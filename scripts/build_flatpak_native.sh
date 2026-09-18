#!/bin/bash
#
# scripts/build_flatpak_native.sh
#
# lgo_download_manager (LGDM) 的**原生架构** Flatpak 打包脚本:在当前机器自己的
# 架构上编译并出单个 .flatpak,不做任何交叉编译。
#
# 用法:
#   ./scripts/build_flatpak_native.sh                     # 在本机架构上出包
#   ./scripts/build_flatpak_native.sh --arch=aarch64      # 只能在 aarch64 机器上跑
#   ./scripts/build_flatpak_native.sh --skip-build        # 复用 bin/lgo_download_manager-<架构>
#   ./scripts/build_flatpak_native.sh --install           # 出包后装进用户安装
#   ./scripts/build_flatpak_native.sh --version=v1.0.0
#
# 产物:
#   dist/lgdm-<版本>-<架构>.flatpak        安装包(flatpak install <文件>)
#   bin/lgo_download_manager-<架构>        打进包里的二进制
#   dist/flatpak/<架构>/{build-dir,repo,state}/   flatpak-builder 的构建目录/仓库/缓存
#   dist/flatpak/<架构>/sysroot/           编译用的 sysroot(指向本架构的 flatpak SDK)
#
# 与 scripts/build_flatpak.sh 的区别 —— 后者可在 x86_64 上用 qemu-user-static +
# gcc-aarch64-linux-gnu 交叉出 aarch64 包;本脚本只出**当前 CPU 架构**的包,并要求
# --arch 与宿主架构一致,交叉编译相关的分支(qemu/binfmt 检查、三元组交叉编译器、
# 多架构循环)全部去掉:
#   - CI 的 ubuntu-24.04 是原生 amd64、ubuntu-24.04-arm 是原生 arm64,没必要求助
#     模拟器;
#   - 原生构建下 cgo 直接用宿主的 gcc/aarch64 本机 GCC,不再需要交叉工具链;
#   - 架构仍然显式传入并在运行时校验:runner 贴错标签会在第一秒报错,而不是
#     安静地出一个架构不符的包。
#
# 注意:**SDK sysroot 这一层不是交叉编译的产物,原生构建也需要**。二进制必须
# 链接它要运行的那个 flatpak runtime 的 libc/GL/X11,而不是宿主的同名库;详见
# prepare_toolchain 的注释。
#
# 依赖:
#   flatpak、flatpak-builder,以及宿主架构的 SDK/runtime:
#     flatpak install --user --arch=<架构> flathub org.gnome.Sdk//50 org.gnome.Platform//50
#   宿主 C 工具链(gcc 或 cc;Fyne 的 GL 绑定是 cgo)。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

MANIFEST="${REPO_ROOT}/org.langbiantianya.LGDM.yml"
APP_ID="org.langbiantianya.LGDM"
BRANCH="stable"
BIN_NAME="lgo_download_manager"
DIST_DIR="${REPO_ROOT}/dist"

ARCH_ARG=""
CC_ARG=""
SKIP_BUILD=false
DO_INSTALL=false
VERSION_OVERRIDE=""

# 颜色输出(与 scripts/ 下其它脚本保持一致)
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_ok()   { echo -e "${GREEN}[OK]${NC} $1"; }
log_step() { echo -e "${YELLOW}[..]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!!]${NC} $1"; }
die()      { echo -e "${RED}[FAIL]${NC} $1" >&2; exit 1; }

usage() {
    sed -n '3,38p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)      usage; exit 0 ;;
        --arch)         ARCH_ARG="${2:-}"; shift 2 ;;
        --arch=*)       ARCH_ARG="${1#*=}"; shift ;;
        --cc)           CC_ARG="${2:-}"; shift 2 ;;
        --cc=*)         CC_ARG="${1#*=}"; shift ;;
        --skip-build)   SKIP_BUILD=true; shift ;;
        --install)      DO_INSTALL=true; shift ;;
        --version)      VERSION_OVERRIDE="${2:-}"; shift 2 ;;
        --version=*)    VERSION_OVERRIDE="${1#*=}"; shift ;;
        *)              die "未知参数: $1(用 --help 看用法)" ;;
    esac
done

# ---------------------------------------------------------------------------
# 基础工具
# ---------------------------------------------------------------------------

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "缺少命令 $1${2:+:$2}"
}

# manifest 是 runtime/sdk 的唯一来源,不在这里重复写死一遍版本号。
manifest_value() {
    sed -n "s/^$1:[[:space:]]*//p" "$MANIFEST" | head -n 1 | tr -d "'\""
}

# 把用户写的架构名收敛成 flatpak 的规范名。
canonical_arch() {
    case "$1" in
        x86_64|amd64|x64) echo "x86_64" ;;
        aarch64|arm64)    echo "aarch64" ;;
        *)                die "不支持的架构: $1(目前只支持 x86_64 / aarch64)" ;;
    esac
}

goarch_of() {
    case "$1" in
        x86_64)  echo "amd64" ;;
        aarch64) echo "arm64" ;;
    esac
}

# GNU 三元组:sysroot 里的多架构库目录名用它(交叉编译器已不需要)。
triplet_of() {
    case "$1" in
        x86_64)  echo "x86_64-linux-gnu" ;;
        aarch64) echo "aarch64-linux-gnu" ;;
    esac
}

# 二进制架构:优先用 readelf 读 ELF 头里的 Machine 字段——file 的输出里带
# 解释器路径(如 /lib64/ld-linux-x86-64.so.2),按关键字匹配会把 aarch64 的
# 二进制误判成 x86_64;没有 readelf 时退回 file,并且只看前两个逗号分段。
binary_arch() {
    local m
    if command -v readelf >/dev/null 2>&1; then
        # LC_ALL=C:readelf/file 的输出会随本地化改变(标签被翻译),英文标签才稳。
        m="$(LC_ALL=C readelf -h "$1" 2>/dev/null | sed -n 's/^[[:space:]]*Machine:[[:space:]]*//p')"
        case "$m" in
            *X86-64*)  echo x86_64; return ;;
            *AArch64*) echo aarch64; return ;;
            "")        : ;;   # 读不出(不是 ELF?),退回 file
            *)         echo unknown; return ;;
        esac
    fi
    case "$(LC_ALL=C file -b "$1" 2>/dev/null | cut -d, -f1-2)" in
        *x86-64*)  echo x86_64 ;;
        *aarch64*) echo aarch64 ;;
        *)         echo unknown ;;
    esac
}

build_version() {
    if [[ -n "$VERSION_OVERRIDE" ]]; then
        echo "$VERSION_OVERRIDE"
        return
    fi
    local v
    v="$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null)" || v=""
    echo "${v:-0.0.0-dev}"
}

# ---------------------------------------------------------------------------
# sysroot / 编译器
# ---------------------------------------------------------------------------

# prepare_toolchain <arch> <real-cc>
#   生成 <dist>/flatpak/<arch>/sysroot(该架构 flatpak SDK 的 Sysroot 视图)
#   与 CC 包装脚本,并回显编译要用的 CC。
#
# 为什么用 flatpak SDK 当 sysroot(原生构建同样如此):
#   - 它就是要运行的 runtime 的开发版,头文件/库/glibc 版本天然对齐
#     (拿宿主的 X11/GL 开发包编译会链接到与 runtime 不同的库版本);
#   - Fyne 的 cgo 依赖(glfw 内嵌 C 源码 + GL/EGL/X11/Xcursor/wayland/xkbcommon)
#     的头、库、pkg-config 文件都在 SDK 里,不需要额外装 -devel 包。
#
# 为什么不直接 gcc --sysroot=<SDK>:
#   flatpak runtime 的 files/ 就是沙箱根(/usr 是符号链接),与 gcc 期望的
#   普通 sysroot 布局不同;而且 SDK 里 libc.so/libm.so 这类是 ld 脚本,里面写的是
#   沙箱内的绝对路径 /usr/lib/... —— 加 --sysroot 反而会被 ld 再拼一次前缀。
#   所以这里搭一个"显式路径"的 sysroot 视图:
#     usr/include -> SDK/include            (头文件,含多架构子目录)
#     usr/lib/... -> SDK 库的符号链接;ld 脚本按 SDK 绝对路径改写
#   再由 CC 包装脚本把 -isystem/-idirafter/-B/-L 指过来。
prepare_toolchain() {
    local arch="$1" real_cc="$2"
    local sdk_dir triplet sdk_lib shim shim_lib wrapper b f
    sdk_dir="$(flatpak info --show-location "runtime/${SDK_NAME}/${arch}/${SDK_BRANCH}")/files"
    [[ -d "$sdk_dir" ]] || die "找不到 ${arch} 的 SDK 目录: $sdk_dir"
    triplet="$(triplet_of "$arch")"
    sdk_lib="${sdk_dir}/lib/${triplet}"
    [[ -d "$sdk_lib" ]] || die "${arch} 的 SDK 里没有 ${triplet} 库目录: $sdk_lib"

    shim="${DIST_DIR}/flatpak/${arch}/sysroot"
    rm -rf "$shim"
    mkdir -p "${shim}/usr"
    ln -s "${sdk_dir}/include" "${shim}/usr/include"
    ln -s "${sdk_dir}/share" "${shim}/usr/share"
    shim_lib="${shim}/usr/lib/${triplet}"
    mkdir -p "$shim_lib"
    # SDK 的 lib/ 下除多架构目录外的内容(如 pkgconfig、扩展)一并链过来,
    # 免得 .pc 里指向 /usr/lib 的路径落空。
    for f in "${sdk_dir}/lib"/*; do
        b="$(basename "$f")"
        [[ "$b" == "$triplet" ]] && continue
        ln -s "$f" "${shim}/usr/lib/${b}"
    done
    for f in "${sdk_lib}"/*; do
        b="$(basename "$f")"
        # 符号链接(真正的 .so.N / .a)直接照链;ld 脚本按 SDK 绝对路径改写。
        # 用管道而不是 $(...):这些文件是二进制的,命令替换会因 NUL 刷一堆告警。
        if [[ -f "$f" && ! -L "$f" ]] && head -c 200 "$f" 2>/dev/null | grep -q 'GNU ld script'; then
            sed -E "s@([[:space:](])/(usr/)?@\1${sdk_dir}/@g" "$f" > "${shim_lib}/${b}"
        else
            ln -s "$f" "${shim_lib}/${b}"
        fi
    done

    # Fedora 的 gcc spec 链接时会追加 -latomic_asneeded / -lgcc_s_asneeded:
    # 这两个名字的文件只存在于 Fedora **本机** gcc 的 libdir 里,SDK 里没有,
    # 缺了就直接以 "ld: 找不到 -latomic_asneeded" 收场(实测踩到)。
    # 按 Fedora 同样的写法补上:只在真正需要时才加 DT_NEEDED。
    if [[ ! -e "${shim_lib}/libatomic_asneeded.so" && -e "${sdk_lib}/libatomic.so" ]]; then
        printf '/* GNU ld script\n   Add DT_NEEDED entry for -latomic only if needed.  */\nINPUT ( AS_NEEDED ( -latomic ) )\n' \
            > "${shim_lib}/libatomic_asneeded.so"
    fi
    if [[ ! -e "${shim_lib}/libgcc_s_asneeded.so" && -e "${sdk_lib}/libgcc_s.so" ]]; then
        printf '/* GNU ld script\n   Add DT_NEEDED entry for -lgcc_s only if needed.  */\nINPUT ( AS_NEEDED ( -lgcc_s ) )\n' \
            > "${shim_lib}/libgcc_s_asneeded.so"
    fi

    wrapper="${DIST_DIR}/flatpak/${arch}/cc-${arch}.sh"
    # 生成脚本里给路径加引号:仓库路径带空格时也能用。
    cat > "$wrapper" <<EOF
#!/bin/sh
# 由 scripts/build_flatpak_native.sh 生成,勿手工改:把编译目标指向本架构 flatpak SDK。
#   -isystem/-idirafter 头文件(后者是多架构目录,bits/、asm/ 在这里)
#   -B                  crt1.o/crti.o/crtn.o 等启动文件
#   -L                  库(其中 libc.so/libm.so 是上面改写过的 ld 脚本,
#                       原始脚本里的 /usr/... 只能在这个视图里解析)
#   -rpath-link         间接依赖(X11/GL/wayland 自己依赖的 libxcb/libXext/libffi…)
#                       去哪找;不给的话 ld 会报一堆 undefined reference
exec "${real_cc}" \
    -isystem "${shim}/usr/include" \
    -idirafter "${shim}/usr/include/${triplet}" \
    -B"${shim}/usr/lib/${triplet}/" \
    -L"${shim}/usr/lib/${triplet}" \
    -Wl,-rpath-link,"${shim}/usr/lib/${triplet}" \
    "\$@"
EOF
    chmod +x "$wrapper"
    echo "$wrapper"
}

# pkgconfig_libdirs <shim>
#   cgo 通过 pkg-config 解析 X11/GL/wayland 的编译链接参数;但这里必须让
#   pkg-config 只看 sysroot 里的 .pc(并让它们输出的路径带上 sysroot 前缀),
#   否则会拿到宿主 /usr 的头文件与库。
pkgconfig_libdirs() {
    local shim="$1" d out=""
    for d in "${shim}/usr/lib/pkgconfig" "${shim}/usr/share/pkgconfig" "${shim}"/usr/lib/*/pkgconfig; do
        [[ -d "$d" ]] && out="${out:+${out}:}${d}"
    done
    echo "$out"
}

# 宿主的 C 编译器(原生构建,不涉及交叉编译器)。
resolve_cc() {
    if [[ -n "$CC_ARG" ]]; then
        if [[ -x "$CC_ARG" ]]; then echo "$CC_ARG"; return; fi
        if command -v "$CC_ARG" >/dev/null 2>&1; then echo "$(command -v "$CC_ARG")"; return; fi
        die "--cc 指向的编译器不可执行: $CC_ARG"
    fi
    local c
    for c in gcc cc; do
        if command -v "$c" >/dev/null 2>&1; then echo "$(command -v "$c")"; return; fi
    done
    die "找不到宿主 C 编译器(gcc/cc):Fyne 的 GL 绑定是 cgo,没有 C 工具链编不过
      Fedora:        sudo dnf install -y gcc
      Debian/Ubuntu: sudo apt install gcc"
}

# ---------------------------------------------------------------------------
# 预检
# ---------------------------------------------------------------------------

require_cmd flatpak
require_cmd flatpak-builder
require_cmd file

[[ -f "$MANIFEST" ]] || die "找不到 manifest: $MANIFEST"

HOST_ARCH="$(flatpak --default-arch)"

# --arch 必须与宿主一致:本脚本是原生构建脚本,不做交叉编译。
if [[ -n "$ARCH_ARG" ]]; then
    REQUESTED="$(canonical_arch "$(echo "$ARCH_ARG" | tr -d '[:space:]')")"
    if [[ "$REQUESTED" != "$HOST_ARCH" ]]; then
        die "--arch=${REQUESTED} 与当前宿主架构 ${HOST_ARCH} 不符:本脚本是原生构建脚本,不做交叉编译。
      在 ${HOST_ARCH} 机器上请用 --arch=${HOST_ARCH},或到 ${REQUESTED} 机器 / runner 上执行。
      (要交叉出包请用 scripts/build_flatpak.sh。)"
    fi
fi
ARCH="$HOST_ARCH"

SDK_NAME="$(manifest_value sdk)"
SDK_BRANCH="$(manifest_value runtime-version)"
RUNTIME_NAME="$(manifest_value runtime)"
[[ -n "$SDK_NAME" && -n "$SDK_BRANCH" && -n "$RUNTIME_NAME" ]] ||
    die "无法从 $MANIFEST 读出 runtime / sdk / runtime-version"

# 该架构的 SDK 与运行时(构建/导出都要用)。
for ref in "runtime/${SDK_NAME}/${ARCH}/${SDK_BRANCH}" "runtime/${RUNTIME_NAME}/${ARCH}/${SDK_BRANCH}"; do
    flatpak info "$ref" >/dev/null 2>&1 || die "缺少 ${ARCH} 的构建依赖 ${ref}
      flatpak install --user --arch=${ARCH} flathub ${SDK_NAME}//${SDK_BRANCH} ${RUNTIME_NAME}//${SDK_BRANCH}
      若报「未发现用于 flathub 的远程引用」:发行版自带的 flathub 可能是**过滤过**的
      (Fedora 就是),只提供本机架构的 ref;加一个用户级未过滤远端再用它装:
        flatpak remote-add --user --if-not-exists flathub-all https://dl.flathub.org/repo/flathub.flatpakrepo
        flatpak install --user --arch=${ARCH} flathub-all ${SDK_NAME}//${SDK_BRANCH} ${RUNTIME_NAME}//${SDK_BRANCH}"
done

# 要自己编二进制时,先确认编译器存在(别编到一半才失败)。
# --skip-build 只复用已有二进制,不需要 go。
if ! $SKIP_BUILD; then
    require_cmd go "Flatpak 包里的二进制要在宿主机上编好(沙箱内未必能访问 module proxy)"
    resolve_cc >/dev/null
fi

VERSION="$(build_version)"

log_step "宿主架构 ${HOST_ARCH};构建 ${ARCH};版本 ${VERSION}"

# ---------------------------------------------------------------------------
# 编二进制 → flatpak-builder → build-bundle
# ---------------------------------------------------------------------------

BIN_PATH="${REPO_ROOT}/bin/${BIN_NAME}-${ARCH}"
BUILD_ROOT="${DIST_DIR}/flatpak/${ARCH}"
OUT_PATH="${DIST_DIR}/lgdm-${VERSION}-${ARCH}.flatpak"

if $SKIP_BUILD; then
    [[ -f "$BIN_PATH" ]] || die "--skip-build 指定为跳过编译,但 $BIN_PATH 不存在"
    log_ok "复用已有二进制 $BIN_PATH"
else
    real_cc="$(resolve_cc)"
    cc="$(prepare_toolchain "$ARCH" "$real_cc")"
    pkg_libdir="$(pkgconfig_libdirs "${BUILD_ROOT}/sysroot")"
    [[ -n "$pkg_libdir" ]] || die "${ARCH} 的 SDK 里没有 pkgconfig 目录,无法解析 X11/GL 依赖"

    COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
    DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    LDFLAGS="-s -w"
    LDFLAGS+=" -X lgo_download_manager/internal/version.Version=${VERSION}"
    LDFLAGS+=" -X lgo_download_manager/internal/version.Commit=${COMMIT}"
    LDFLAGS+=" -X lgo_download_manager/internal/version.Date=${DATE}"

    log_step "go build → bin/${BIN_NAME}-${ARCH}(CC=$(basename "$real_cc"),sysroot=${BUILD_ROOT}/sysroot)"
    mkdir -p "${REPO_ROOT}/bin"
    # PKG_CONFIG_SYSROOT_DIR/PKG_CONFIG_LIBDIR 让 pkg-config 只在 sysroot 里找,
    # 并把 -I/-L 前缀改写成 sysroot 下的路径;PKG_CONFIG_PATH 清空,避免宿主的
    # .pc 混进来。GOOS/GOARCH/CGO_ENABLED 显式给出,与宿主架构一致。
    (
        cd "$REPO_ROOT"
        GOOS=linux GOARCH="$(goarch_of "$ARCH")" CGO_ENABLED=1 CC="$cc" \
        PKG_CONFIG_SYSROOT_DIR="${BUILD_ROOT}/sysroot" \
        PKG_CONFIG_LIBDIR="$pkg_libdir" \
        PKG_CONFIG_PATH= \
            go build -trimpath -ldflags "$LDFLAGS" -o "$BIN_PATH" .
    )
    log_ok "二进制 $BIN_PATH"
fi

actual="$(binary_arch "$BIN_PATH")"
if [[ "$actual" == "unknown" ]]; then
    log_warn "识别不出 $BIN_PATH 的架构,跳过架构校验"
elif [[ "$actual" != "$ARCH" ]]; then
    die "$BIN_PATH 的架构是 ${actual},与目标 ${ARCH} 不符;
      重新编译,或删掉 bin/ 下不匹配的旧文件"
fi

rm -f "$OUT_PATH"
# 上一次构建被 Ctrl-C / kill 掉时,flatpak-builder 的 rofiles-fuse 会留下
# 悬挂挂载点与 rofiles 缓存,下次构建直接以
#   Failure spawning rofiles-fuse ... Device or resource busy / Permission denied
# 失败(实测踩到过)。这里只清理本次要用的 state 目录下的残留。
if [[ -d "${BUILD_ROOT}/state/rofiles" ]]; then
    for m in "${BUILD_ROOT}/state/rofiles"/*; do
        [[ -e "$m" ]] || continue
        command -v fusermount >/dev/null 2>&1 && fusermount -u -z "$m" 2>/dev/null || true
        rm -rf "$m" 2>/dev/null || true
    done
fi

log_step "flatpak-builder --arch=${ARCH} → ${BUILD_ROOT}"
(
    cd "$REPO_ROOT"
    flatpak-builder \
        --force-clean \
        --arch="$ARCH" \
        --repo="${BUILD_ROOT}/repo" \
        --state-dir="${BUILD_ROOT}/state" \
        "${BUILD_ROOT}/build-dir" \
        "$MANIFEST"
)

log_step "flatpak build-bundle → $OUT_PATH"
flatpak build-bundle \
    --arch="$ARCH" \
    "${BUILD_ROOT}/repo" \
    "$OUT_PATH" \
    "$APP_ID" \
    "$BRANCH"
log_ok "${ARCH} 安装包 $OUT_PATH ($(du -h "$OUT_PATH" | cut -f1))"

# ---------------------------------------------------------------------------
# 可选安装
# ---------------------------------------------------------------------------

if $DO_INSTALL; then
    log_step "flatpak install --user $OUT_PATH"
    flatpak install --user --noninteractive --assumeyes "$OUT_PATH"
    log_ok "已安装到用户安装(${APP_ID}//${BRANCH},${ARCH})"
fi

echo
echo "本次产物:"
echo "  ${OUT_PATH}"
echo
echo "安装: flatpak install --user dist/lgdm-${VERSION}-${ARCH}.flatpak"
echo "运行: flatpak run ${APP_ID}"
echo "卸载: flatpak uninstall --user ${APP_ID}"
