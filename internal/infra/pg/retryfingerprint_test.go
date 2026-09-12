package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// A retry is the same turn whatever time it carries. An adapter that stamps the time it
// stores a turn stamps a retry three seconds later with another time; it replays the observation
// already held, whose first time stands. Different words under the same key are still a conflict.
func TestARetryIsTheSameTurnWhateverTimeItCarries(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retry_time")
	store := pg.NewObservationStore(pool)
	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	turn := func(at time.Time, content string) domain.Turn {
		return domain.Turn{Scope: "p1", DataSubjectID: "subject-1", OccurredAt: at,
			Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: content, OccurredAt: at}}}
	}

	key := uuid.NewString()
	original, replayed, err := store.AppendIdempotent(ctx, schema, turn(first, "I moved to Oslo last month."), key)
	if err != nil || replayed {
		t.Fatalf("the first attempt: %v replayed=%v", err, replayed)
	}
	retry, replayed, err := store.AppendIdempotent(ctx, schema, turn(first.Add(3*time.Second), "I moved to Oslo last month."), key)
	if err != nil || !replayed || retry.ID != original.ID || !retry.OccurredAt.Equal(first) {
		t.Fatalf("a retry three seconds later was not the same turn: %+v replayed=%v err=%v", retry, replayed, err)
	}
	if _, _, err := store.AppendIdempotent(ctx, schema, turn(first, "I moved to Bergen last month."), key); !errors.Is(err, pg.ErrIdempotencyConflict) {
		t.Fatalf("different words under the same key were accepted: %v", err)
	}

}
