// Package plugin defines the worker backend plugin contract. A plugin is a
// subprocess invoked inside a Daedalus pod that receives a task envelope on
// stdin (or a mounted file) and signals progress, human-input requests, and
// completion through defined interfaces.
//
// The contract is the subprocess protocol, not a Go import boundary — most
// plugins (Claude Code, future qwen-coder, etc.) are written in whatever
// language their underlying tool is built in. This package exists so that
// Minos and in-repo Go tests can reason about the contract in-process.
//
// Subprocess protocol summary (authoritative per architecture.md §8):
//
//  1. Pod is spawned with the envelope JSON available at the path given
//     by the DAEDALUS_ENVELOPE environment variable (or on stdin when
//     the env var is unset — plugin author's choice at the contract
//     boundary, Minos supports both).
//  2. Plugin performs work. Status, thinking, and code-block updates go
//     through the thread sidecar's MCP at DAEDALUS_THREAD_URL. Human
//     input requests are the same MCP's request_human_input.
//  3. On SIGTERM, plugin has 30 seconds to flush memory extraction to
//     the shared volume at /var/run/daedalus/memory/ before SIGKILL.
//  4. Exit code 0 = success; nonzero = failure. Minos marks the task
//     accordingly and persists whatever memory blob was flushed.
package plugin
