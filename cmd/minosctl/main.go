// Command minosctl is the operator CLI for commissioning tasks directly
// against Minos, short-circuiting the Hermes/Discord intake path for
// Phase 1 Slice A testing per docs/phase-1-plan.md §4.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// Environment variables read by minosctl. Keeping the token out of argv
// avoids exposing it in shell history and process listings.
const (
	envMinosURL   = "MINOS_URL"
	envMinosToken = "MINOS_ADMIN_TOKEN"
)

func main() {
	root := &cobra.Command{
		Use:           "minosctl",
		Short:         "Operator CLI for the Minos control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(commissionCmd())
	root.AddCommand(listCmd())
	root.AddCommand(getCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func commissionCmd() *cobra.Command {
	var (
		brief         string
		detail        string
		repo          string
		branch        string
		baseBranch    string
		workspaceSize string
		taskType      string
	)
	cmd := &cobra.Command{
		Use:   "commission",
		Short: "Commission a new task",
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := core.CommissionRequest{
				TaskType: envelope.TaskType(taskType),
				Brief:    envelope.Brief{Summary: brief, Detail: detail},
				Execution: core.ExecutionRequest{
					RepoURL:       repo,
					Branch:        branch,
					BaseBranch:    baseBranch,
					WorkspaceSize: envelope.WorkspaceSize(workspaceSize),
				},
				Origin: envelope.Origin{
					Surface:   "internal",
					RequestID: fmt.Sprintf("minosctl-%d", time.Now().UnixNano()),
					Requester: os.Getenv("USER"),
				},
			}
			resp, err := do(cmd.Context(), http.MethodPost, "/tasks", req)
			if err != nil {
				return err
			}
			return printJSON(resp)
		},
	}
	cmd.Flags().StringVar(&brief, "brief", "", "one-line task brief (required)")
	cmd.Flags().StringVar(&detail, "detail", "", "markdown task detail")
	cmd.Flags().StringVar(&repo, "repo", "", "target repo URL (required)")
	cmd.Flags().StringVar(&branch, "branch", "", "feature branch (required)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "base branch (default: project default)")
	cmd.Flags().StringVar(&workspaceSize, "workspace-size", "", "small | medium | large (default: project default)")
	cmd.Flags().StringVar(&taskType, "task-type", "code", "task type (code | inference-tuning)")
	_ = cmd.MarkFlagRequired("brief")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("branch")
	return cmd
}

func listCmd() *cobra.Command {
	var state string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := "/tasks"
			if state != "" {
				path += "?state=" + state
			}
			resp, err := do(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			return printJSON(resp)
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "filter by state (queued,running,completed,failed)")
	return cmd
}

func getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <task-id>",
		Short: "Show a single task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := do(cmd.Context(), http.MethodGet, "/tasks/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(resp)
		},
	}
}

// do builds and executes an authenticated HTTP request against Minos.
func do(ctx context.Context, method, path string, body any) ([]byte, error) {
	base := os.Getenv(envMinosURL)
	if base == "" {
		return nil, errors.New(envMinosURL + " not set")
	}
	token := os.Getenv(envMinosToken)
	if token == "" {
		return nil, errors.New(envMinosToken + " not set")
	}

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, fmt.Errorf("encode body: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("minos responded %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func printJSON(data []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		// Fall back to raw bytes if it's not valid JSON.
		_, _ = os.Stdout.Write(data)
		return nil
	}
	_, _ = pretty.WriteTo(os.Stdout)
	fmt.Println()
	return nil
}
