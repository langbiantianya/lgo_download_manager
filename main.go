// ldm (Local Download Manager) is the single binary that runs the full
// download manager: SQLite store, scheduler, gRPC service, and Fyne GUI.
//
// Threading model:
//
//	main goroutine            Fyne app.Run() + event loop
//	  │                          fyne.Do() for thread-safe GUI updates
//	  ▼
//	Scheduler                  owns task map + background flusher goroutine
//	  │
//	  ├── Store                SQLite via modernc.org/sqlite
//	  ├── Engine               concurrent WriteAt over pre-allocated files
//	  └── Protocol drivers     HTTP / FTP / WebDAV
//
//	gRPC service (:50051)      separate goroutine; AddTask calls Scheduler.Add
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"fyne.io/fyne/v2/app"

	"lgo_download_manager/internal/grpcsvc"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/ui"
)

const (
	defaultDBPath   = "ldm.sqlite"
	defaultGRPCAddr = ":50051"
)

func main() {
	dbPath := flag.String("db", defaultDBPath, "path to SQLite database")
	grpcAddr := flag.String("grpc", defaultGRPCAddr, "gRPC listen address")
	noGUI := flag.Bool("no-gui", false, "start without the Fyne GUI (gRPC service only)")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Store
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// Scheduler
	sc := scheduler.New(st)
	go sc.Run(ctx) // background flusher; cancelled via ctx

	// gRPC server — runs until ctx is cancelled
	grpcErr := make(chan error, 1)
	go func() {
		if err := grpcsvc.Register(ctx, *grpcAddr, sc); err != nil {
			grpcErr <- fmt.Errorf("grpc: %w", err)
		}
	}()

	// GUI — must run on the main goroutine where Fyne's Run() executes.
	if !*noGUI {
		a := app.NewWithID("com.ldm")
		// Construct the window AFTER the app is created so widget constructors
		// can resolve fyne.CurrentApp() (list.go calls it during setup).
		win := ui.NewMainWindow(a, sc)
		win.SetGRPCStatus(fmt.Sprintf("已启动 (%s)", *grpcAddr))
		win.Show()
		// a.Run() blocks the main goroutine and drives the Fyne event loop.
		a.Run()
		cancel()
	}

	// Wait for shutdown signal or gRPC error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Println("shutting down...")
		cancel()
	case err := <-grpcErr:
		log.Fatal(err)
	}
}

// defaultSaveDir returns a platform-appropriate download directory.
func defaultSaveDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows", "darwin":
		return filepath.Join(home, "Downloads")
	default:
		return filepath.Join(home, "Downloads")
	}
}