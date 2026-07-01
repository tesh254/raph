package exporter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raph/internal/db"
	"raph/internal/knowledge"
	"raph/internal/memory"
)

func newStore(t *testing.T) *db.LibSQLStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestDocumentMarkdownAndWrite(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	doc, err := knowledge.Add(ctx, store, nil, knowledge.AddInput{
		Workspace: "ws", Title: "My Arch", DocType: knowledge.DocArchitecture,
		Content: "the design", NoEmbed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	art, err := Document(ctx, store, doc.Node.ID, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Content, "doc_type: architecture") || !strings.Contains(art.Content, "# My Arch") {
		t.Fatalf("markdown export missing frontmatter/title:\n%s", art.Content)
	}

	dir := t.TempDir()
	target, err := Write(art, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(target) != dir {
		t.Fatalf("expected file written into %s, got %s", dir, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("export file missing: %v", err)
	}
}

// TestBrainExportImportRoundTrip exports memory, a rule, and a handoff, then
// imports the JSON into a fresh store and confirms each comes back intact.
func TestBrainExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newStore(t)
	if _, err := memory.Store(ctx, src, nil, memory.StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "decision", Title: "Use libsql",
		Content: "we standardized on libsql", Source: "cli", WriterID: "tester", MemoryKey: "use-libsql",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ctx, src, nil, memory.StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "rule", Title: "No self-credit",
		Content: "never self-credit in commits", Source: "cli", WriterID: "tester", MemoryKey: "no-self-credit",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledge.Add(ctx, src, nil, knowledge.AddInput{
		Workspace: "ws", Title: "Handoff 1", DocType: knowledge.DocHandoff,
		Content: "what to do next", NoEmbed: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A non-handoff doc must NOT be exported.
	if _, err := knowledge.Add(ctx, src, nil, knowledge.AddInput{
		Workspace: "ws", Title: "Arch", DocType: knowledge.DocArchitecture,
		Content: "design doc", NoEmbed: true,
	}); err != nil {
		t.Fatal(err)
	}

	art, err := Brain(ctx, src, []string{"global", "shared"}, FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Content, "\"raph_export_version\"") ||
		!strings.Contains(art.Content, "\"memory\"") || strings.Contains(art.Content, "design doc") {
		t.Fatalf("brain json malformed or leaked non-handoff doc: %s", art.Content)
	}

	dst := newStore(t)
	res, err := Import(ctx, dst, nil, []byte(art.Content), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Memory != 2 || res.Handoffs != 1 {
		t.Fatalf("expected 2 memory + 1 handoff, got memory=%d handoffs=%d", res.Memory, res.Handoffs)
	}
	rule, err := dst.GetMemoryRecordByKey(ctx, "global", "global", "rule", "no-self-credit")
	if err != nil {
		t.Fatalf("imported rule missing: %v", err)
	}
	if !strings.Contains(rule.Node.Content, "never self-credit") {
		t.Fatalf("rule content not intact: %q", rule.Node.Content)
	}

	// The handoff must be restored under its original workspace ("ws"), not
	// dumped into the global workspace (regression guard for the json:"-"
	// workspace-drop bug).
	inWS, err := knowledge.List(ctx, dst, knowledge.ListFilter{Workspace: "ws", DocType: knowledge.DocHandoff})
	if err != nil {
		t.Fatal(err)
	}
	if len(inWS) != 1 {
		t.Fatalf("handoff not restored to workspace 'ws': got %d", len(inWS))
	}
	inGlobal, err := knowledge.List(ctx, dst, knowledge.ListFilter{Workspace: knowledge.GlobalWorkspace, DocType: knowledge.DocHandoff})
	if err != nil {
		t.Fatal(err)
	}
	if len(inGlobal) != 0 {
		t.Fatalf("handoff leaked into global workspace: got %d", len(inGlobal))
	}
}

// TestParseEnvelopeRejectsNewer guards the version gate.
func TestParseEnvelopeRejectsNewer(t *testing.T) {
	_, err := ParseEnvelope([]byte(`{"raph_export_version":9999,"kind":"brain","memory":[{"memory_key":"x"}]}`))
	if err == nil {
		t.Fatal("expected newer-version envelope to be rejected")
	}
}
