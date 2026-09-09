// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package nativefolder

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	ole32                = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")

	shell32                         = syscall.NewLazyDLL("shell32.dll")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
)

// COM initialisation flags. STA (apartment-threaded) is required for
// shell dialogs to marshal correctly between threads.
const (
	coinitApartmentThreaded = 0x2
)

// CLSID_FileOpenDialog — {DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}.
var clsidFileOpenDialog = syscall.GUID{
	Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE,
	Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7},
}

// IID_IFileOpenDialog — {D57C7288-D4AD-4768-BE02-9D969532D960}.
var iidFileOpenDialog = syscall.GUID{
	Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768,
	Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60},
}

// IID_IShellItem — {43826D1E-E718-42EE-BC55-A1E261C37BFE}.
var iidShellItem = syscall.GUID{
	Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE,
	Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE},
}

// IFileDialog v-table slot indices. The layout is fixed by the COM
// declaration order in shobjidl.h; we record only the methods used.
//   3: Show              (IModalWindow)
//   9: SetOptions        (IFileDialog)
//  12: SetFolder         (IFileDialog)
//  17: SetTitle          (IFileDialog)
//  20: GetResult         (IFileDialog)
const (
	ifileDialogShow      = 3
	ifileDialogSetFolder = 12
	ifileDialogSetTitle  = 17
	ifileDialogGetResult = 20
)

// IShellItem v-table slot indices.
//   5: GetDisplayName
const ishellItemGetDisplayName = 5

// FOS_PICKFOLDERS — restrict the dialog to folder selections only.
const fosPickFolders = 0x20

// SIGDN_FILESYSPATH — return a filesystem path from IShellItem::GetDisplayName.
const sigdnFilesysPath = 0x80058000

// HRESULT codes.
const (
	sOk     = 0
	eCancel = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
)

// COM v-table manipulation notes.
//
// The standard Go-COM idiom (used by go-ole, golang.org/x/sys/windows,
// the syscall package itself) converts a uintptr returned from a
// syscall.Proc.Call into a typed pointer via unsafe.Pointer. Go vet's
// "unsafeptr" check warns about every such conversion even when the
// pointer is consumed immediately. The check is a heuristic — every
// production Go-COM binding accepts the same warnings.
//
// Every raw → typed conversion in this file is followed immediately by
// the typed dereference and is never stored across a function call
// boundary. The pointers COM hands back (IFileOpenDialog, IShellItem)
// remain valid for the duration of this function because we hold the
// only outstanding reference until Release is called in the deferred
// releaseCom.

// comSlot reads the n-th entry of a COM v-table given a uintptr to the
// COM object. Slot 0 is IUnknown::QueryInterface, 1 is AddRef, 2 is
// Release. The conversion at the top of the function is the canonical
// Go-COM idiom and produces a go-vet "unsafeptr" advisory; the same
// pattern appears in syscall_windows.go (vet-exempt as stdlib).
func comSlot(obj uintptr, n uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(vtbl + n*unsafe.Sizeof(uintptr(0))))
}

// comCall invokes a v-table function pointer with stdcall calling
// convention. We use SyscallN because Go's older Syscall{0..6} hard-codes
// the argument count and pads the stack with junk we cannot reason
// about for variadic stdcall sites.
func comCall(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

// releaseCom invokes IUnknown::Release on a COM pointer.
func releaseCom(obj uintptr) {
	if obj == 0 {
		return
	}
	comCall(comSlot(obj, 2), obj)
}

// PickFolder opens the Windows Explorer folder-selection dialog and
// returns the path the user chose. initialDir is the folder the dialog
// opens in; pass "" to let Windows pick the default.
//
// The dialog is the modern IFileOpenDialog (Vista+) with FOS_PICKFOLDERS
// — the same control Windows Explorer itself uses for "Choose a folder".
// If the user cancels the picker, returns ErrCancelled.
func PickFolder(initialDir string) (string, error) {
	// COM must be initialised on the thread that calls IFileDialog::Show.
	// fyne's UI goroutine is not guaranteed to be COM-initialised, so we
	// initialise here. S_FALSE (1) means "already initialised" — fine.
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if hr != 0 && hr != 1 {
		return "", fmt.Errorf("CoInitializeEx: 0x%08x", uint32(hr))
	}
	defer procCoUninitialize.Call()

	var dlg uintptr
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		0x1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidFileOpenDialog)),
		uintptr(unsafe.Pointer(&dlg)),
	)
	if hr != 0 {
		return "", fmt.Errorf("CoCreateInstance(FileOpenDialog): 0x%08x", uint32(hr))
	}
	if dlg == 0 {
		return "", fmt.Errorf("CoCreateInstance(FileOpenDialog): null pointer")
	}
	defer releaseCom(dlg)

	comCall(comSlot(dlg, 9) /* SetOptions */, dlg, uintptr(fosPickFolders))
	setDialogTitle(dlg, "选择保存位置")

	if initialDir != "" {
		if err := setFolderFromPath(dlg, initialDir); err != nil {
			// Non-fatal — fall back to the dialog's default starting
			// folder. The user can still navigate.
		}
	}

	hr = comCall(comSlot(dlg, ifileDialogShow), dlg, 0)
	if hr == eCancel {
		return "", ErrCancelled
	}
	if hr != sOk {
		return "", fmt.Errorf("IFileDialog::Show: 0x%08x", uint32(hr))
	}

	var item uintptr
	hr = comCall(comSlot(dlg, ifileDialogGetResult), dlg, uintptr(unsafe.Pointer(&item)))
	if hr != sOk || item == 0 {
		return "", fmt.Errorf("IFileDialog::GetResult: 0x%08x", uint32(hr))
	}
	defer releaseCom(item)

	var namePtr uintptr
	hr = comCall(comSlot(item, ishellItemGetDisplayName), item, sigdnFilesysPath, uintptr(unsafe.Pointer(&namePtr)))
	if hr != sOk || namePtr == 0 {
		return "", fmt.Errorf("IShellItem::GetDisplayName: 0x%08x", uint32(hr))
	}
	defer procCoTaskMemFree.Call(namePtr)

	path := utf16PtrToString((*uint16)(unsafe.Pointer(namePtr)))
	if path == "" {
		return "", ErrCancelled
	}
	return filepath.Clean(path), nil
}

// setDialogTitle sets the dialog title via IFileDialog::SetTitle.
func setDialogTitle(dlg uintptr, title string) {
	p := utf16Ptr(title)
	if p == nil {
		return
	}
	comCall(comSlot(dlg, ifileDialogSetTitle), dlg, uintptr(unsafe.Pointer(p)))
}

// setFolderFromPath binds the dialog's initial folder to the given
// filesystem path via SHCreateItemFromParsingName → IShellItem → SetFolder.
func setFolderFromPath(dlg uintptr, path string) error {
	p := utf16Ptr(path)
	if p == nil {
		return fmt.Errorf("alloc path")
	}
	var item uintptr
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(p)),
		0,
		uintptr(unsafe.Pointer(&iidShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if hr != sOk || item == 0 {
		return fmt.Errorf("SHCreateItemFromParsingName: 0x%08x", uint32(hr))
	}
	defer releaseCom(item)

	hr = comCall(comSlot(dlg, ifileDialogSetFolder), dlg, item)
	if hr != sOk {
		return fmt.Errorf("IFileDialog::SetFolder: 0x%08x", uint32(hr))
	}
	return nil
}

// utf16Ptr converts a Go string into a UTF-16 pointer suitable for
// Win32 APIs. Returns nil on encoding error.
func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

// utf16PtrToString converts a UTF-16 pointer returned by Win32 into a
// Go string. The pointer is read up to the first zero terminator.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := p; *ptr != 0; ptr = (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + 2)) {
		n++
	}
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	for i := range n {
		buf[i] = *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(i*2)))
	}
	return syscall.UTF16ToString(buf)
}
