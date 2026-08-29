//go:build ignore
// +build ignore

// Minimal pure-Go FTP test server for protocol tests.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var rootDir = os.TempDir()

func main() {
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--root" && i+1 < len(os.Args) {
			rootDir = os.Args[i+1]
			i++
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Listen error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	fmt.Fprintf(os.Stderr, "FTP server ready on 127.0.0.1:%d, root=%s\n", ln.Addr().(*net.TCPAddr).Port, rootDir)

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleConn(conn)
	}
}

type session struct {
	mu          sync.Mutex
	loggedIn    bool
	renameFrom  string
	restOffset  int64
	currentPath string
	dataConn    net.Conn
	dataReady   *sync.Cond
	dataListener net.Listener
}

func newSession() *session {
	s := &session{currentPath: "/"}
	s.dataReady = sync.NewCond(&s.mu)
	return s
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	writer := bufio.NewWriter(conn)
	s := newSession()

	fmt.Fprintf(writer, "220 FTP Test Server (Go)\r\n")
	writer.Flush()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(os.Stderr, "[%s] %s\n", conn.RemoteAddr(), line)

		cmd := strings.ToUpper(strings.TrimSpace(line))
		if len(cmd) < 4 {
			continue
		}
		code := cmd[:4]

		switch {
		case strings.HasPrefix(code, "USER"):
			fmt.Fprintf(writer, "331 Username ok, send password.\r\n")
		case strings.HasPrefix(code, "PASS"):
			fmt.Fprintf(writer, "230 Login successful.\r\n")
			s.mu.Lock()
			s.loggedIn = true
			s.mu.Unlock()
		case strings.HasPrefix(code, "QUIT"):
			fmt.Fprintf(writer, "221 Goodbye.\r\n")
			writer.Flush()
			return
		case strings.HasPrefix(code, "NOOP"):
			fmt.Fprintf(writer, "200 OK.\r\n")
		case strings.HasPrefix(code, "SYST"):
			fmt.Fprintf(writer, "215 UNIX Type: L8\r\n")
		case strings.HasPrefix(code, "FEAT"):
			fmt.Fprintf(writer, "211-Features:\r\n REST\r\n211 End\r\n")
		case strings.HasPrefix(code, "TYPE"):
			fmt.Fprintf(writer, "200 Type set to I\r\n")
		case strings.HasPrefix(code, "PWD"), cmd == "XPWD":
			fmt.Fprintf(writer, "257 \"%s\"\r\n", s.currentPath)
		case strings.HasPrefix(code, "CWD"):
			s.mu.Lock()
			if !s.loggedIn {
				s.mu.Unlock()
				fmt.Fprintf(writer, "530 Not logged in.\r\n")
				continue
			}
			path := strings.TrimSpace(line[4:])
			if !strings.HasPrefix(path, "/") {
				path = filepath.Join(s.currentPath, path)
			}
			info, err := os.Stat(filepath.Join(rootDir, path))
			if err != nil || !info.IsDir() {
				s.mu.Unlock()
				fmt.Fprintf(writer, "550 Not a directory.\r\n")
				continue
			}
			s.currentPath = filepath.Clean(path)
			if s.currentPath == "." {
				s.currentPath = "/"
			}
			s.mu.Unlock()
			fmt.Fprintf(writer, "250 CWD successful.\r\n")
		case cmd == "PASV":
			s.mu.Lock()
			if !s.loggedIn {
				s.mu.Unlock()
				fmt.Fprintf(writer, "530 Not logged in.\r\n")
				continue
			}
			// Close any previous data connection
			if s.dataConn != nil {
				s.dataConn.Close()
				s.dataConn = nil
			}
			if s.dataListener != nil {
				s.dataListener.Close()
				s.dataListener = nil
			}
			// Listen on a new data port
			dataLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				s.mu.Unlock()
				fmt.Fprintf(writer, "425 Can't open data connection.\r\n")
				continue
			}
			s.dataListener = dataLn
			dataPort := dataLn.Addr().(*net.TCPAddr).Port
			fmt.Fprintf(writer, "227 Entering Passive Mode (127,0,0,1,%d,%d).\r\n",
				dataPort/256, dataPort%256)
			writer.Flush()
			// Accept data connection asynchronously
			go func() {
				conn2, err := dataLn.Accept()
				dataLn.Close()
				s.mu.Lock()
				if err == nil {
					s.dataConn = conn2
				}
				s.dataReady.Signal()
				s.mu.Unlock()
			}()
			s.mu.Unlock()
		case strings.HasPrefix(code, "LIST"):
			s.mu.Lock()
			if !s.loggedIn {
				s.mu.Unlock()
				fmt.Fprintf(writer, "530 Not logged in.\r\n")
				continue
			}
			// Wait for data connection if PASV was issued
			if s.dataListener != nil && s.dataConn == nil {
				s.dataReady.Wait()
			}
			if s.dataConn == nil {
				s.mu.Unlock()
				fmt.Fprintf(writer, "425 No data connection.\r\n")
				continue
			}
			dataConn := s.dataConn
			s.dataConn = nil
			s.mu.Unlock()
			entries, _ := os.ReadDir(filepath.Join(rootDir, s.currentPath))
			for _, e := range entries {
				info, _ := e.Info()
				var mode string
				if info.IsDir() {
					mode = "drwxr-xr-x"
				} else {
					mode = "-rw-r--r--"
				}
				size := int64(0)
				if info != nil {
					size = info.Size()
				}
				fmt.Fprintf(dataConn, "%s 1 0 0 %12d Jan  1 00:00 %s\r\n",
					mode, size, e.Name())
			}
			dataConn.Close()
			fmt.Fprintf(writer, "226 Transfer complete.\r\n")
		case strings.HasPrefix(code, "RETR"):
			s.mu.Lock()
			if !s.loggedIn {
				s.mu.Unlock()
				fmt.Fprintf(writer, "530 Not logged in.\r\n")
				continue
			}
			// Wait for data connection if PASV was issued
			if s.dataListener != nil && s.dataConn == nil {
				s.dataReady.Wait()
			}
			if s.dataConn == nil {
				s.mu.Unlock()
				fmt.Fprintf(writer, "425 No data connection.\r\n")
				continue
			}
			dataConn := s.dataConn
			s.dataConn = nil
			path := strings.TrimSpace(line[5:])
			s.mu.Unlock()
			if strings.HasPrefix(path, "/") {
				path = path[1:]
			}
			fullPath := filepath.Join(rootDir, path)
			f, err := os.Open(fullPath)
			if err != nil {
				fmt.Fprintf(writer, "550 File not found.\r\n")
				if dataConn != nil {
					dataConn.Close()
				}
				continue
			}
			defer f.Close()
			s.mu.Lock()
			if s.restOffset > 0 {
				f.Seek(s.restOffset, 0)
				s.restOffset = 0
			}
			s.mu.Unlock()
			fmt.Fprintf(writer, "150 Opening data connection.\r\n")
			writer.Flush()
			io.Copy(dataConn, f)
			dataConn.Close()
			fmt.Fprintf(writer, "226 Transfer complete.\r\n")
		case strings.HasPrefix(code, "SIZE"):
			s.mu.Lock()
			if !s.loggedIn {
				s.mu.Unlock()
				fmt.Fprintf(writer, "530 Not logged in.\r\n")
				continue
			}
			path := strings.TrimSpace(line[5:])
			s.mu.Unlock()
			if strings.HasPrefix(path, "/") {
				path = path[1:]
			}
			fullPath := filepath.Join(rootDir, path)
			info, err := os.Stat(fullPath)
			if err != nil {
				fmt.Fprintf(writer, "550 Could not get file size.\r\n")
				continue
			}
			fmt.Fprintf(writer, "213 %d\r\n", info.Size())
		case strings.HasPrefix(code, "REST"):
			s.mu.Lock()
			offset, _ := strconv.ParseInt(strings.TrimSpace(line[5:]), 10, 64)
			s.restOffset = offset
			s.mu.Unlock()
			fmt.Fprintf(writer, "350 Restart position accepted.\r\n")
		case strings.HasPrefix(code, "RNFR"):
			s.mu.Lock()
			s.renameFrom = strings.TrimSpace(line[5:])
			s.mu.Unlock()
			fmt.Fprintf(writer, "350 Ready for RNTO.\r\n")
		case strings.HasPrefix(code, "RNTO"):
			s.mu.Lock()
			s.renameFrom = ""
			s.mu.Unlock()
			fmt.Fprintf(writer, "250 RNTO successful.\r\n")
		case strings.HasPrefix(code, "DELE"):
			fmt.Fprintf(writer, "250 Deleted.\r\n")
		case strings.HasPrefix(code, "MKD"):
			fmt.Fprintf(writer, "257 Created.\r\n")
		case strings.HasPrefix(code, "RMD"):
			fmt.Fprintf(writer, "250 Removed.\r\n")
		default:
			fmt.Fprintf(writer, "502 Command not implemented.\r\n")
		}
		writer.Flush()
	}
}
