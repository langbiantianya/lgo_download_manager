// ldm (Local Download Manager) is the single binary that runs the full
// download manager: SQLite store, scheduler, and Fyne GUI.
//
// URL scheme: lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"fyne.io/fyne/v2/app"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/ui"
	"lgo_download_manager/internal/urllauncher"
)

const (
	defaultDBPath = "ldm.sqlite"
)

func main() {
	dbPath := flag.String("db", defaultDBPath, "path to SQLite database")
	noGUI := flag.Bool("no-gui", false, "start without the Fyne GUI")
	openURL := flag.String("open-url", "", "download URL (lgom://... format)")
	flag.Parse()

	// Collect URLs from --open-url flag and positional arguments.
	// Some desktop environments pass the URL as a positional arg.
	var urls []string
	if *openURL != "" {
		urls = append(urls, *openURL)
	}
	for _, arg := range flag.Args() {
		if strings.HasPrefix(arg, "lgom://") {
			urls = append(urls, arg)
		}
	}

	// Single-instance lock
	isPrimary, release, err := urllauncher.AcquireLock()
	if err != nil {
		log.Fatalf("urllauncher: %v", err)
	}
	if !isPrimary {
		// Another instance is running; forward all URLs and exit
		if len(urls) > 0 {
			for _, u := range urls {
				if err := urllauncher.SendURL(u); err != nil {
					log.Fatalf("failed to forward URL: %v", err)
				}
			}
			log.Printf("forwarded %d URL(s) to primary instance", len(urls))
		} else {
			log.Println("another instance is already running")
		}
		os.Exit(0)
		return
	}
	defer release()

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

	// URL handler: add download task from URL request
	handleDownloadURL := func(rawURL string) {
		req, err := urllauncher.HandleURL(rawURL)
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
		}
		log.Printf("adding download: %s", req.URL)

		// Detect protocol
		proto, err := protocol.DetectKind(req.URL, "")
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
		}

		// Use default save dir, or configured one
		saveDir := defaultSaveDir()
		if ui.GlobalSettings.DefaultSaveDir != "" {
			saveDir = ui.GlobalSettings.DefaultSaveDir
		}

		// Filename from URL if not provided
		filename := req.Name
		if filename == "" {
			filename = filepath.Base(req.URL)
			// Remove query string from filename
			if idx := strings.IndexByte(filename, '?'); idx != -1 {
				filename = filename[:idx]
			}
		}
		savePath := filepath.Join(saveDir, filename)

		auth := protocol.AuthOptions{
			UserAgent: req.UA,
			Cookies:   req.Cookies,
		}

		tk, err := sc.Add(scheduler.AddTaskInput{
			URL:         req.URL,
			SavePath:    savePath,
			Protocol:    proto,
			Auth:        auth,
			ChunkCount:  4,
			MinChunkSize: ui.GlobalSettings.MinChunkSize,
		})
		if err != nil {
			log.Printf("failed to add task: %v", err)
			return
		}

		// Start the download
		if err := sc.Start(tk.ID); err != nil {
			log.Printf("failed to start task: %v", err)
		}
	}

	// Start Unix socket server for URL forwarding (non-blocking)
	go func() {
		if err := urllauncher.ListenAndServe(handleDownloadURL); err != nil {
			log.Printf("urllauncher server error: %v", err)
		}
	}()

	// Handle URLs passed as arguments (via --open-url or positional args)
	for _, u := range urls {
		handleDownloadURL(u)
	}

	// GUI — must run on the main goroutine where Fyne's Run() executes.
	if !*noGUI {
		a := app.NewWithID("com.ldm")
		// Construct the window AFTER the app is created so widget constructors
		// can resolve fyne.CurrentApp() (list.go calls it during setup).
		win := ui.NewMainWindow(a, st, sc)
		win.Show()
		a.Run()
		cancel()
		os.Exit(0)
	}

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	cancel()
}

// defaultSaveDir returns a platform-appropriate download directory.
func defaultSaveDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}
