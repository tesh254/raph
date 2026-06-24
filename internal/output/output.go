// Package output centralizes the text/JSON rendering decision for CLI commands.
// Agents that shell out to raph get machine-readable JSON automatically; humans
// running a command in a terminal get readable text. An explicit flag always
// wins so either audience can override per invocation.
package output

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Resolve picks the output format. Precedence:
//  1. explicit flag value ("json" / "text")
//  2. RAPH_FORMAT / RAPH_JSON environment (agent attribution)
//  3. stdout is not a terminal (piped or captured) -> JSON
//  4. default -> text
func Resolve(explicit string) Format {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case "json":
		return FormatJSON
	case "text":
		return FormatText
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("RAPH_FORMAT")), "json") {
		return FormatJSON
	}
	if os.Getenv("RAPH_JSON") == "1" {
		return FormatJSON
	}
	if !stdoutIsTerminal() {
		return FormatJSON
	}
	return FormatText
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Print writes value as indented JSON when format is JSON, otherwise invokes the
// provided text renderer.
func Print(w io.Writer, format Format, value any, text func(io.Writer) error) error {
	if format == FormatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	return text(w)
}
