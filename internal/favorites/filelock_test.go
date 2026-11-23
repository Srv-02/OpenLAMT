package favorites

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Srv-02/openlamt/internal/keychain"
)

// TestConcurrentManagersLoseNothing: two Managers on one file stand for the app and
// openlamt-mcp. Every Upsert from many goroutines must survive.
func TestConcurrentManagersLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	mgrs := []*Manager{
		NewManager(NewStore(path), keychain.NewMemory()),
		NewManager(NewStore(path), keychain.NewMemory()),
	}
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := Favorite{Name: "d", Host: fmt.Sprintf("h%d.example.com", i), Username: "admin"}
			if err := mgrs[i%2].Upsert(f); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, err := mgrs[0].List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != n {
		t.Fatalf("got %d favorites, want %d: updates were lost", len(list), n)
	}
}

// TestChangeLoginVersusSetQuality: a rename by one process and a field change by another
// must both stay in the file, so the favorite keeps the new user name.
func TestChangeLoginVersusSetQuality(t *testing.T) {
	for round := 0; round < 25; round++ {
		path := filepath.Join(t.TempDir(), "favorites.json")
		keysA, keysB := keychain.NewMemory(), keychain.NewMemory()
		a := NewManager(NewStore(path), keysA)
		b := NewManager(NewStore(path), keysB)
		if err := a.Upsert(fav); err != nil {
			t.Fatal(err)
		}
		keysA.Set(fav.Host, "admin", "old")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := a.ChangeLogin(fav.Host, "admin", "root", "new"); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			// Either order is valid: before the rename it finds admin, after it root.
			if err := b.SetQuality(fav.Host, "admin", QualityLow); err != nil && err != ErrNotFound {
				t.Error(err)
			}
		}()
		wg.Wait()
		list, err := a.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].Username != "root" {
			t.Fatalf("round %d: rename lost: %+v", round, list)
		}
	}
}
