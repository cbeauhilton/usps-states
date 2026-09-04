package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CacheDir holds fetched bodies so development does not hammer the publisher.
// Gitignored: the committed copy of the authority lives in evidence/, which is
// the fixture the tests read.
const CacheDir = ".cache"

// DefaultTTL is how long a cached body is considered fresh. Short, because the
// point of `verify` is to ask the authority; the cache exists so that editing
// the generator does not mean forty round trips to pe.usps.com.
const DefaultTTL = time.Hour

// FetchOpts controls where a body comes from.
type FetchOpts struct {
	Refresh bool // always go to the network
	Offline bool // never go to the network; use the cache at any age
	TTL     time.Duration
}

type cacheMeta struct {
	Source
	CachedAt string `json:"cached_at"`
}

func cachePaths(url string) (body, meta string) {
	sum := sha256.Sum256([]byte(url))
	base := filepath.Join(CacheDir, hex.EncodeToString(sum[:])[:16])
	return base + ".body", base + ".json"
}

// readCache returns the cached body and its recorded provenance, plus how old
// it is. Age is meaningless when ok is false.
func readCache(url string) (body []byte, src Source, age time.Duration, ok bool) {
	bp, mp := cachePaths(url)
	b, err := os.ReadFile(bp)
	if err != nil {
		return nil, Source{}, 0, false
	}
	m, err := os.ReadFile(mp)
	if err != nil {
		return nil, Source{}, 0, false
	}
	var cm cacheMeta
	if json.Unmarshal(m, &cm) != nil {
		return nil, Source{}, 0, false
	}
	t, err := time.Parse(time.RFC3339, cm.CachedAt)
	if err != nil {
		return nil, Source{}, 0, false
	}
	// A cache entry whose body no longer matches its recorded digest is not a
	// cache entry, it is a corruption. Refetch rather than proceed.
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != cm.SHA256 {
		return nil, Source{}, 0, false
	}
	return b, cm.Source, time.Since(t), true
}

func writeCache(url string, body []byte, src Source) error {
	if err := os.MkdirAll(CacheDir, 0o755); err != nil {
		return err
	}
	bp, mp := cachePaths(url)
	if err := os.WriteFile(bp, body, 0o644); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cacheMeta{Source: src, CachedAt: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mp, append(b, '\n'), 0o644)
}

// fetchCached is fetch() with a local cache in front of it.
//
// It always says which it used. A tool that silently answers from a cache is
// how "we checked the authority" becomes a claim nobody can trust — the whole
// reason this repository exists is that three surveys reported a page as gone
// without opening it.
func fetchCached(o FetchOpts) ([]byte, Source, error) {
	ttl := o.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if o.Offline && o.Refresh {
		return nil, Source{}, fmt.Errorf("-offline and -refresh are contradictory")
	}

	if !o.Refresh {
		body, src, age, ok := readCache(SourceURL)
		switch {
		case ok && o.Offline:
			fmt.Printf("  CACHED (offline, %s old) %s\n", age.Round(time.Second), SourceURL)
			return body, src, nil
		case ok && age < ttl:
			fmt.Printf("  CACHED (%s old, ttl %s) %s\n  pass -refresh to ask the authority\n",
				age.Round(time.Second), ttl, SourceURL)
			return body, src, nil
		case !ok && o.Offline:
			return nil, Source{}, fmt.Errorf("-offline but %s holds no valid entry for %s — "+
				"run once without -offline", CacheDir, SourceURL)
		}
	}

	body, src, err := fetch()
	if err != nil {
		// Falling back to a stale cache would turn an outage into a false
		// "verified". Say what happened and let the caller decide.
		if _, _, age, ok := readCache(SourceURL); ok {
			return nil, Source{}, fmt.Errorf("%w\n  a cached copy %s old exists; re-run with -offline "+
				"to use it, knowing it was not checked against the authority", err, age.Round(time.Second))
		}
		return nil, Source{}, err
	}
	fmt.Printf("  LIVE %s\n", SourceURL)
	if err := writeCache(SourceURL, body, src); err != nil {
		fmt.Fprintf(os.Stderr, "  (cache write failed: %v)\n", err)
	}
	return body, src, nil
}
