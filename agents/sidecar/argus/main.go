// Command zakros-argus-sidecar runs as a sidecar container in every
// Daedalus pod and emits a heartbeat to the Argus binary's
// /argus/heartbeat endpoint on a configurable interval. The sidecar
// is separate from the worker backend per architecture.md §8 Pod
// Sidecars so a hung or compromised worker cannot suppress its
// heartbeat.
//
// Slice J: heartbeat ingest moved off Minos and onto the extracted
// Argus binary. The pod's MCP_AUTH_TOKEN JWT carries audience=argus +
// scope=heartbeat; Argus's brokerauth.Verifier checks both.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
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

	argusURL := strings.TrimRight(os.Getenv("ZAKROS_ARGUS_INGEST_URL"), "/")
	taskID := os.Getenv("ZAKROS_TASK_ID")
	token := os.Getenv("MCP_AUTH_TOKEN")
	if argusURL == "" || taskID == "" || token == "" {
		log.Fatal("ZAKROS_ARGUS_INGEST_URL, ZAKROS_TASK_ID, and MCP_AUTH_TOKEN must all be set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	url := argusURL + "/argus/heartbeat"
	body, err := json.Marshal(map[string]string{"task_id": taskID})
	if err != nil {
		log.Fatalf("encode heartbeat body: %v", err)
	}
	client := &http.Client{Timeout: *timeout}

	// Send one immediately so Argus sees the sidecar alive without waiting
	// a full interval.
	sendBeat(ctx, client, url, token, body)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("argus sidecar exiting: %v", ctx.Err())
			return
		case <-ticker.C:
			sendBeat(ctx, client, url, token, body)
		}
	}
}

func sendBeat(ctx context.Context, client *http.Client, url, token string, body []byte) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("argus sidecar: build request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
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
