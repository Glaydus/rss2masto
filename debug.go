package rss2masto

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-ps"
)

var debugMode = strings.HasPrefix(filepath.Base(os.Args[0]), "__debug_bin")

func init() {
	if !debugMode {
		debugMode = isDebugging()
	}
}

func isDebugging() bool {
	pid := os.Getppid()

	// We loop in case there were intermediary processes like the gopls language server.
	for pid != 0 {
		switch p, err := ps.FindProcess(pid); {
		case err != nil:
			return false
		case strings.HasPrefix(p.Executable(), "dlv"):
			return true
		default:
			pid = p.PPid()
		}
	}
	return false
}
