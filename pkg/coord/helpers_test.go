package coord

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFilterNumbersPlaceholdersInOrder(t *testing.T) {
	var f filter
	set := "name = " + f.arg("ada")
	f.add("deleted_at IS NULL")
	f.add("(id ILIKE ? OR name ILIKE ?)", "%x%", "%y%")
	f.add("version = ?", 3)
	limit := f.limit(newPage(2, 10, 20))

	if set != "name = $1" {
		t.Errorf("arg placeholder: %s", set)
	}
	if got := f.where(); got != " WHERE deleted_at IS NULL AND (id ILIKE $2 OR name ILIKE $3) AND version = $4" {
		t.Errorf("where: %s", got)
	}
	if limit != " LIMIT $5 OFFSET $6" {
		t.Errorf("limit: %s", limit)
	}
	if !slices.Equal(f.args, []any{"ada", "%x%", "%y%", 3, 10, 10}) {
		t.Errorf("args: %v", f.args)
	}
	var empty filter
	if empty.where() != "" {
		t.Error("no conditions renders no WHERE")
	}
}

func TestPageNormalization(t *testing.T) {
	p := newPage(0, 0, 25)
	if p.Number != 1 || p.Size != 25 || p.offset() != 0 {
		t.Fatalf("defaults: %+v", p)
	}
	if got := newPage(3, 10, 25); got.offset() != 20 || got.totalPages(21) != 3 || got.totalPages(0) != 0 {
		t.Fatalf("page math: %+v", got)
	}
}

func TestRunIDFor(t *testing.T) {
	generated, err := runIDFor("")
	if err != nil || uuid.MustParse(generated).Version() != 7 {
		t.Fatalf("generated run ID must be UUIDv7: %s %v", generated, err)
	}
	fresh, _ := uuid.NewV7()
	if got, err := runIDFor(fresh.String()); err != nil || got != fresh.String() {
		t.Fatalf("a fresh UUIDv7 is accepted: %v", err)
	}

	old := uuidV7At(time.Now().Add(-48 * time.Hour))
	for _, bad := range []string{"order-1", uuid.NewString(), old.String()} {
		if _, err := runIDFor(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", bad, err)
		}
	}
}

// uuidV7At returns a UUIDv7 whose 48-bit millisecond timestamp is t.
func uuidV7At(t time.Time) uuid.UUID {
	id, _ := uuid.NewV7()
	ms := uint64(t.UnixMilli())
	for i := range 6 {
		id[i] = byte(ms >> (40 - 8*i))
	}
	return id
}

func TestSmallHelpers(t *testing.T) {
	if nullJSON(nil) != nil || nullString("") != nil || *nullString("x") != "x" {
		t.Error("empty values must map to SQL NULL")
	}
	if !errors.Is(orStale(nil), errStaleSnapshot) || orStale(errors.New("db")) == errStaleSnapshot {
		t.Error("orStale only replaces a nil error")
	}
	if sinceOrZero(time.Time{}) != 0 || sinceOrZero(time.Now().Add(-time.Second)) < time.Second {
		t.Error("sinceOrZero")
	}
}
