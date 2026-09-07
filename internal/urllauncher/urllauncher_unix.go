// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows

package urllauncher

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
)

// AcquireLock attempts to acquire an exclusive flock on the lock file.
// Returns isPrimary=true if this instance holds the lock.
// Returns isPrimary=false, release=nil, err=nil if another instance holds the lock.
func AcquireLock() (isPrimary bool, release func(), err error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return false, nil, fmt.Errorf("creating base dir: %w", err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return false, nil, fmt.Errorf("opening lock file: %w", err)
	}

	// Try to acquire exclusive flock (non-blocking with F_SETLK)
	lockErr := syscall.FcntlFlock(uintptr(lockFile.Fd()), syscall.F_SETLK, &syscall.Flock_t{
		Type:   syscall.F_WRLCK,
		Whence: 0,
		Start:  0,
		Len:    0,
	})
	if lockErr != nil {
		// Another instance holds the lock
		lockFile.Close()
		return false, nil, nil
	}

	release = func() {
		syscall.FcntlFlock(uintptr(lockFile.Fd()), syscall.F_SETLK, &syscall.Flock_t{
			Type:   syscall.F_UNLCK,
			Whence: 0,
			Start:  0,
			Len:    0,
		})
		lockFile.Close()
		os.Remove(lockPath)
	}
	return true, release, nil
}

// ListenAndServe starts a Unix socket server on socketPath and calls onURL
// for each received URL.
func ListenAndServe(onURL func(url string)) error {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("creating base dir: %w", err)
	}

	// Remove stale socket
	os.Remove(socketPath)

	ln, err := net.ListenUnix("unix", &net.UnixAddr{
		Name: socketPath,
		Net:  "unix",
	})
	if err != nil {
		return fmt.Errorf("listen unix: %w", err)
	}

	// Set socket permissions (world-accessible for forwarding)
	syscall.Chmod(socketPath, 0777)

	var wg sync.WaitGroup
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			return fmt.Errorf("accept unix: %w", err)
		}
		wg.Add(1)
		go func(c *net.UnixConn) {
			defer wg.Done()
			defer c.Close()
			handleConn(c, onURL)
		}(conn)
	}
}

func handleConn(c *net.UnixConn, onURL func(url string)) {
	// Read 4-byte big-endian length prefix
	var length uint32
	if err := binary.Read(c, binary.BigEndian, &length); err != nil {
		return
	}

	if length > 1024*1024 { // 1 MB max
		return
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(c, buf); err != nil {
		return
	}

	var msg struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(buf, &msg); err != nil {
		return
	}

	if msg.URL != "" {
		onURL(msg.URL)
	}
}

// SendURL connects to the socket and sends the URL using length-prefixed JSON.
func SendURL(url string) error {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{
		Name: socketPath,
		Net:  "unix",
	})
	if err != nil {
		return fmt.Errorf("dial unix: %w", err)
	}
	defer conn.Close()

	msg := struct {
		URL string `json:"url"`
	}{URL: url}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

	if err := binary.Write(conn, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}
	return nil
}