package crawler

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/verbose"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
)

type DocumentationCrawler struct {
	collector   *colly.Collector
	store       db.GraphStore
	cfg         *config.Config
	seedURL     string
	hostname    string
	basePrefix  string
	corpusID    string
	versionID   string
	workspaceID string
	runCtx      context.Context
	knownPages  map[string]struct{}
	followLinks bool

	mu       sync.Mutex
	firstErr error
	pageErr  error
	stats    Stats
}

type Stats struct {
	PagesIndexed      int `json:"pages_indexed"`
	ChunksIndexed     int `json:"chunks_indexed"`
	EmbeddingsCreated int `json:"embeddings_created"`
	PagesFailed       int `json:"pages_failed,omitempty"`
}

func NewDocumentationCrawler(store db.GraphStore, cfg *config.Config, rawURL string) (*DocumentationCrawler, error) {
	return newDocumentationCrawler(store, cfg, rawURL, true)
}

func NewSinglePageCrawler(store db.GraphStore, cfg *config.Config, rawURL string) (*DocumentationCrawler, error) {
	return newDocumentationCrawler(store, cfg, rawURL, false)
}

func newDocumentationCrawler(store db.GraphStore, cfg *config.Config, rawURL string, followLinks bool) (*DocumentationCrawler, error) {
	verbose.Printf("creating crawler url=%s followLinks=%t", rawURL, followLinks)
	if store == nil {
		return nil, fmt.Errorf("graph store is required")
	}
	if cfg != nil {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
	}

	seedURL, parsedURL, err := normalizeSeedURL(rawURL)
	if err != nil {
		return nil, err
	}

	collector := colly.NewCollector(
		colly.AllowedDomains(parsedURL.Hostname()),
		colly.MaxDepth(3),
		colly.Async(true),
	)
	// SSRF hardening: refuse connections to internal/loopback addresses on every
	// hop (redirects included), cap response bodies, and bound each request.
	collector.WithTransport(safeHTTPTransport())
	collector.MaxBodySize = crawlMaxBodySize
	collector.SetRequestTimeout(crawlRequestTimeout)
	if err := collector.Limit(&colly.LimitRule{
		DomainRegexp: regexp.QuoteMeta(parsedURL.Hostname()),
		Parallelism:  2,
		RandomDelay:  1 * time.Second,
	}); err != nil {
		return nil, fmt.Errorf("configure crawler rate limit: %w", err)
	}

	c := &DocumentationCrawler{
		collector:   collector,
		store:       store,
		cfg:         cfg,
		seedURL:     seedURL,
		hostname:    parsedURL.Hostname(),
		basePrefix:  prefixBase(parsedURL),
		corpusID:    corpusID(parsedURL),
		versionID:   crawlVersionID(seedURL),
		workspaceID: workspaceID(parsedURL),
		runCtx:      context.Background(),
		knownPages:  make(map[string]struct{}),
		followLinks: followLinks,
	}
	c.registerCallbacks()
	return c, nil
}

func (c *DocumentationCrawler) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *DocumentationCrawler) WorkspaceID() string {
	return c.workspaceID
}

func (c *DocumentationCrawler) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.runCtx = ctx

	verbose.Printf("registering web corpus")
	if err := c.registerCorpus(ctx); err != nil {
		return err
	}
	verbose.Printf("ensuring site node")
	if err := c.ensureSiteNode(ctx); err != nil {
		return err
	}

	verbose.Printf("visiting seed url=%s", c.seedURL)
	if err := c.collector.Visit(c.seedURL); err != nil {
		return err
	}
	verbose.Printf("waiting for crawler to finish")
	c.collector.Wait()

	if err := c.getErr(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// If not a single page made it in, the crawl effectively failed (bad seed,
	// SSRF-blocked host, everything 4xx/5xx) — surface the first page error.
	c.mu.Lock()
	pageErr, indexed, failed := c.pageErr, c.stats.PagesIndexed, c.stats.PagesFailed
	c.mu.Unlock()
	if indexed == 0 && pageErr != nil {
		return pageErr
	}
	if failed > 0 {
		verbose.Printf("crawl run complete with %d skipped page(s)", failed)
	}
	verbose.Printf("crawl run complete")
	return nil
}

func (c *DocumentationCrawler) registerCorpus(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if err := c.store.SaveWebCorpus(ctx, db.WebCorpus{
		ID:        c.corpusID,
		ScopeType: "web",
		ScopeID:   c.basePrefix,
		Source:    "crawl",
		BaseURL:   c.basePrefix,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return fmt.Errorf("save web corpus: %w", err)
	}
	if err := c.store.SaveWebCrawlVersion(ctx, db.WebCrawlVersion{
		ID:        c.versionID,
		CorpusID:  c.corpusID,
		SeedURL:   c.seedURL,
		CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("save web crawl version: %w", err)
	}
	return nil
}

func (c *DocumentationCrawler) registerCallbacks() {
	c.collector.OnRequest(func(r *colly.Request) {
		if err := c.getErr(); err != nil {
			r.Abort()
			return
		}
		if err := c.runCtx.Err(); err != nil {
			c.setErr(err)
			r.Abort()
			return
		}
	})

	c.collector.OnError(func(r *colly.Response, err error) {
		// Context cancellation aborts the whole run.
		if c.runCtx != nil && c.runCtx.Err() != nil {
			c.setErr(c.runCtx.Err())
			return
		}
		failedURL := ""
		if r != nil && r.Request != nil && r.Request.URL != nil {
			failedURL = r.Request.URL.String()
		}
		// A single dead link or transient 5xx must not fail an otherwise-good
		// crawl. Record it as a counted warning; Run() only surfaces an error if
		// nothing at all was indexed (e.g. a bad or SSRF-blocked seed).
		verbose.Printf("crawl page error (skipped) url=%s err=%v", failedURL, err)
		c.mu.Lock()
		c.stats.PagesFailed++
		if c.pageErr == nil {
			if failedURL != "" {
				c.pageErr = fmt.Errorf("crawl %s: %w", failedURL, err)
			} else {
				c.pageErr = err
			}
		}
		c.mu.Unlock()
	})

	c.collector.OnHTML("html", func(e *colly.HTMLElement) {
		if err := c.getErr(); err != nil {
			return
		}

		pageURL, err := normalizeTargetURL(e.Request.URL.String())
		if err != nil {
			c.setErr(err)
			return
		}

		verbose.Printf("processing page url=%s", pageURL)
		markdown, err := c.extractMarkdown(e, pageURL)
		if err != nil {
			c.setErr(fmt.Errorf("extract markdown for %s: %w", pageURL, err))
			return
		}

		title := strings.TrimSpace(e.DOM.Find("title").First().Text())
		if title == "" {
			title = pageURL
		}

		ctx := c.runCtx

		if err := c.store.SaveNode(ctx, db.Node{
			ID:        pageURL,
			Workspace: c.workspaceID,
			Domain:    "documentation",
			Type:      "doc_page",
			Name:      title,
			Content:   markdown,
			URL:       pageURL,
		}); err != nil {
			c.setErr(fmt.Errorf("save page node %s: %w", pageURL, err))
			return
		}
		if err := c.attachPageToSite(ctx, pageURL); err != nil {
			c.setErr(fmt.Errorf("attach page %s to site: %w", pageURL, err))
			return
		}

		c.markPageKnown(pageURL)
		c.recordPage()
		verbose.Printf("page saved url=%s title=%s", pageURL, title)

		if err := c.ingestSections(ctx, pageURL, title, markdown); err != nil {
			c.setErr(err)
			return
		}

		if !c.followLinks {
			return
		}

		e.ForEach("a[href]", func(_ int, el *colly.HTMLElement) {
			if err := c.getErr(); err != nil {
				return
			}
			href := strings.TrimSpace(el.Attr("href"))
			if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") || strings.HasPrefix(strings.ToLower(href), "mailto:") {
				return
			}

			absolute := e.Request.AbsoluteURL(href)
			if absolute == "" {
				return
			}
			absolute, err = normalizeTargetURL(absolute)
			if err != nil {
				return
			}
			if !c.shouldTraverse(absolute) {
				return
			}

			if err := c.ensurePlaceholderPageNode(ctx, absolute); err != nil {
				c.setErr(fmt.Errorf("save placeholder page %s: %w", absolute, err))
				return
			}
			if err := c.attachPageToSite(ctx, absolute); err != nil {
				c.setErr(fmt.Errorf("attach placeholder page %s to site: %w", absolute, err))
				return
			}
			if err := c.store.SaveEdge(ctx, db.Edge{SourceID: pageURL, TargetID: absolute, Type: "LINKS_TO"}); err != nil {
				c.setErr(fmt.Errorf("save link edge %s -> %s: %w", pageURL, absolute, err))
				return
			}
			_ = e.Request.Visit(absolute)
		})
	})
}

func (c *DocumentationCrawler) ingestSections(ctx context.Context, pageURL string, pageTitle string, markdown string) error {
	sections := splitMarkdownSections(markdown, pageTitle)
	verbose.Printf("ingesting sections page=%s count=%d", pageURL, len(sections))
	for idx, section := range sections {
		if strings.TrimSpace(section.Content) == "" {
			continue
		}

		var embedding []float32
		if c.cfg != nil && c.cfg.HasEmbeddingProvider() {
			var err error
			embedding, err = config.GenerateEmbedding(ctx, c.cfg, section.Title+"\n\n"+section.Content)
			if err != nil {
				return fmt.Errorf("generate embedding for %s section %d: %w", pageURL, idx+1, err)
			}
		}

		chunkID := chunkID(pageURL, idx)
		if err := c.store.SaveNode(ctx, db.Node{
			ID:        chunkID,
			Workspace: c.workspaceID,
			Domain:    "documentation",
			Type:      "markdown_chunk",
			Name:      section.Title,
			Content:   section.Content,
			URL:       pageURL,
			Embedding: embedding,
		}); err != nil {
			return fmt.Errorf("save markdown chunk %s: %w", chunkID, err)
		}
		if err := c.store.SaveEdge(ctx, db.Edge{SourceID: pageURL, TargetID: chunkID, Type: "HAS_SECTION"}); err != nil {
			return fmt.Errorf("save section edge %s -> %s: %w", pageURL, chunkID, err)
		}
		c.recordChunk(len(embedding) > 0)
	}
	return nil
}

func (c *DocumentationCrawler) extractMarkdown(e *colly.HTMLElement, pageURL string) (string, error) {
	selectors := []string{"main", "article", ".content", "#content", "body"}

	for _, selector := range selectors {
		selection := e.DOM.Find(selector).First()
		if selection.Length() == 0 {
			continue
		}
		html, err := sanitizedSelectionHTML(selection)
		if err != nil {
			continue
		}
		markdown, err := htmltomarkdown.ConvertString(html, converter.WithDomain(pageURL))
		if err != nil {
			continue
		}
		markdown = cleanMarkdown(markdown)
		if markdown != "" {
			return markdown, nil
		}
	}

	return "", fmt.Errorf("no usable content body found")
}

func (c *DocumentationCrawler) ensurePlaceholderPageNode(ctx context.Context, absoluteURL string) error {
	if c.hasKnownPage(absoluteURL) {
		return nil
	}
	return c.store.SaveNode(ctx, db.Node{
		ID:        absoluteURL,
		Workspace: c.workspaceID,
		Domain:    "documentation",
		Type:      "doc_page",
		Name:      absoluteURL,
		Content:   "",
		URL:       absoluteURL,
	})
}

func (c *DocumentationCrawler) ensureSiteNode(ctx context.Context) error {
	return c.store.SaveNode(ctx, db.Node{
		ID:        siteNodeID(c.basePrefix),
		Workspace: c.workspaceID,
		Domain:    "documentation",
		Type:      "doc_site",
		Name:      siteDisplayName(c.seedURL),
		Content:   c.basePrefix,
		URL:       c.basePrefix,
	})
}

func (c *DocumentationCrawler) attachPageToSite(ctx context.Context, pageURL string) error {
	return c.store.SaveEdge(ctx, db.Edge{SourceID: siteNodeID(c.basePrefix), TargetID: pageURL, Type: "HAS_PAGE"})
}

func (c *DocumentationCrawler) shouldTraverse(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil {
		return false
	}
	if parsed.Hostname() != c.hostname {
		return false
	}
	return insideBasePrefix(target, c.basePrefix)
}

func (c *DocumentationCrawler) setErr(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.firstErr == nil {
		c.firstErr = err
	}
}

func (c *DocumentationCrawler) getErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.firstErr
}

func (c *DocumentationCrawler) markPageKnown(pageURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.knownPages[pageURL] = struct{}{}
}

func (c *DocumentationCrawler) hasKnownPage(pageURL string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.knownPages[pageURL]
	return ok
}

func (c *DocumentationCrawler) recordPage() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.PagesIndexed++
}

func (c *DocumentationCrawler) recordChunk(embedded bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.ChunksIndexed++
	if embedded {
		c.stats.EmbeddingsCreated++
	}
}

type markdownSection struct {
	Title   string
	Content string
}

func splitMarkdownSections(markdown string, fallbackTitle string) []markdownSection {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return nil
	}

	parts := strings.Split("\n"+markdown, "\n## ")
	sections := make([]markdownSection, 0, len(parts))
	for idx, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx > 0 {
			part = "## " + part
		}

		title := fallbackTitle
		lines := strings.Split(part, "\n")
		if len(lines) > 0 {
			first := strings.TrimSpace(lines[0])
			if strings.HasPrefix(first, "## ") {
				title = strings.TrimSpace(strings.TrimPrefix(first, "## "))
			}
		}
		if title == "" {
			title = fmt.Sprintf("Section %d", len(sections)+1)
		}

		sections = append(sections, markdownSection{Title: title, Content: part})
	}
	return sections
}

func sanitizedSelectionHTML(selection *goquery.Selection) (string, error) {
	clone := selection.Clone()
	clone.Find("script, style, noscript, svg").Remove()
	return goquery.OuterHtml(clone)
}

func cleanMarkdown(markdown string) string {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	markdown = strings.TrimSpace(markdown)
	for strings.Contains(markdown, "\n\n\n") {
		markdown = strings.ReplaceAll(markdown, "\n\n\n", "\n\n")
	}
	return markdown
}

func normalizeSeedURL(raw string) (string, *url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", nil, fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", nil, fmt.Errorf("url must include scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", nil, fmt.Errorf("url scheme must be http or https")
	}
	parsed.Fragment = ""
	return normalizeURL(parsed), parsed, nil
}

func normalizeTargetURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse target url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("target url must include scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("target url scheme must be http or https")
	}
	parsed.Fragment = ""
	return normalizeURL(parsed), nil
}

func normalizeURL(u *url.URL) string {
	copyURL := *u
	if copyURL.Path == "" {
		copyURL.Path = "/"
	}
	if copyURL.Path != "/" {
		copyURL.Path = strings.TrimRight(copyURL.Path, "/")
		if copyURL.Path == "" {
			copyURL.Path = "/"
		}
	}
	return copyURL.String()
}

func prefixBase(u *url.URL) string {
	normalized := normalizeURL(u)
	if strings.HasSuffix(normalized, "/") {
		return strings.TrimSuffix(normalized, "/")
	}
	return normalized
}

func insideBasePrefix(target string, base string) bool {
	if target == base {
		return true
	}
	return strings.HasPrefix(target, base+"/") || strings.HasPrefix(target, base+"?")
}

func siteNodeID(basePrefix string) string {
	h := sha1.Sum([]byte(basePrefix))
	return "doc_site:" + hex.EncodeToString(h[:])
}

func siteDisplayName(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return raw
	}
	return u.Hostname()
}

func workspaceID(u *url.URL) string {
	h := sha1.Sum([]byte(prefixBase(u)))
	return "crawl:" + hex.EncodeToString(h[:])
}

func corpusID(u *url.URL) string {
	h := sha1.Sum([]byte(prefixBase(u)))
	return "web_corpus:" + hex.EncodeToString(h[:])
}

func crawlVersionID(seedURL string) string {
	h := sha1.Sum([]byte(seedURL + "|" + time.Now().UTC().Format(time.RFC3339Nano)))
	return "web_crawl:" + hex.EncodeToString(h[:])
}

func chunkID(pageURL string, index int) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s#%d", pageURL, index)))
	return "markdown_chunk:" + hex.EncodeToString(h[:])
}
