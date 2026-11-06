package tag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// Entry holds one functional card's tags. Name and CardID are there for
// people reading the file; the store key is what identifies the card.
type Entry struct {
	Name   string `json:"name"`
	CardID string `json:"card_id"` // one printing, for reference
	Tags   []Tag  `json:"tags"`
	// Reviewed is set once a person has checked the tags. Retagging
	// keeps human tags and leaves reviewed entries alone.
	Reviewed bool `json:"reviewed,omitempty"`
}

// Store maps functional cards to their tags. It is safe for concurrent
// use. Saved files sort by key, so they diff cleanly.
type Store struct {
	mu           sync.Mutex
	VocabVersion string            `json:"vocab_version"`
	Cards        map[string]*Entry `json:"cards"`
}

// KeyOf is the store key for a card: a short hash of its FunctionalKey,
// shared by every reprint.
func KeyOf(c *catalog.Card) string {
	sum := sha256.Sum256([]byte(c.FunctionalKey()))
	return hex.EncodeToString(sum[:8])
}

// LoadStore reads a store file. A missing file gives an empty store.
func LoadStore(path string) (*Store, error) {
	s := &Store{VocabVersion: Vocab().Version, Cards: map[string]*Entry{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("reading tag store %s: %w", path, err)
	}
	if s.Cards == nil {
		s.Cards = map[string]*Entry{}
	}
	return s, nil
}

// Save writes the store atomically.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	b, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tags-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Get returns a card's entry, or nil when it has not been tagged.
func (s *Store) Get(c *catalog.Card) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Cards[KeyOf(c)]
}

// Put sets a card's tags, keeping any human tags already stored.
// Reviewed entries are left unchanged.
func (s *Store) Put(c *catalog.Card, tags []Tag) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := KeyOf(c)
	old := s.Cards[key]
	if old != nil && old.Reviewed {
		return
	}
	var human []Tag
	if old != nil {
		for _, t := range old.Tags {
			if t.Source == Human {
				human = append(human, t)
			}
		}
	}
	s.Cards[key] = &Entry{Name: c.Name, CardID: c.ID, Tags: Merge(tags, human)}
}
