// Command daedalus-argus-sidecar runs as a sidecar container in every
// Daedalus pod and emits a heartbeat to Minos's /tasks/{id}/heartbeat
// endpoint on a configurable interval. The sidecar is separate from the
// worker backend per architecture.md §8 Pod Sidecars so a hung or
// compromised worker cannot suppress its heartbeat.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	interval := flag.Duration("interval", 30*time.Second, "heartbeat interval")
	timeout := flag.Duration("timeout", 5*time.Second, "per-heartbeat HTTP timeout")
	flag.Parse()

	minosURL := strings.TrimRight(os.Getenv("DAEDALUS_MINOS_URL"), "/")
	taskID := os.Getenv("DAEDALUS_TASK_ID")
	token := os.Getenv("MCP_AUTH_TOKEN")
	if minosURL == "" || taskID == "" || token == "" {
		log.Fatal("DAEDALUS_MINOS_URL, DAEDALUS_TASK_ID, and MCP_AUTH_TOKEN must all be set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	url := fmt.Sprintf("%s/tasks/%s/heartbeat", minosURL, taskID)
	client := &http.Client{Timeout: *timeout}

	// Send one immediately so Minos sees the sidecar alive without waiting
	// a full interval.
	sendBeat(ctx, client, url, token)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("argus sidecar exiting: %v", ctx.Err())
			return
		case <-ticker.C:
			sendBeat(ctx, client, url, token)
		}
	}
}

func sendBeat(ctx context.Context, client *http.Client, url, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		log.Printf("argus sidecar: build request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("argus sidecar: heartbeat: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("argus sidecar: heartbeat status %d", resp.StatusCode)
	}
}
