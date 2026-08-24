//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/query"
	"github.com/shardit-io/vv/repo/basic"
)

// A UUID primary key is a different animal from an integer one, and every layer
// touches it differently: the schema reader has to accept a [16]byte array as a
// column, the preloader has to key its index by it, the update planner has to
// compare two of them, and the DSL has to turn the string a client sends into
// one. None of that follows from the integer tests.
//
// The shape below is the one ent produces for a UUID-keyed schema: an array PK
// with no database default, a defined string type for an enum, and a *string for
// an Optional().Nillable() column.

type RoomKind string

const (
	RoomDirect RoomKind = "direct"
	RoomGroup  RoomKind = "group"
)

type Room struct {
	ID        uuid.UUID `db:"id,pk,noauto"`
	CreatedAt time.Time `db:"created_at"`
	Kind      RoomKind  `db:"kind"`
	Name      *string   `db:"name"`

	Members []RoomMember `rel:"has_many,fk=RoomID"`
}

type RoomUpdate struct {
	Kind *RoomKind
	Name crud.Opt[string]
}

type RoomMember struct {
	ID     uuid.UUID           `db:"id,pk,noauto"`
	RoomID uuid.UUID           `db:"room_id"`
	Role   *string             `db:"role"`
	Left   crud.Opt[time.Time] `db:"left_at"`

	Room *Room `rel:"belongs_to,fk=RoomID"`
}

type RoomMemberUpdate struct {
	Role *string
	Left crud.Opt[time.Time]
}

var (
	Rooms       = basic.Define[Room, uuid.UUID, RoomUpdate]("uu_rooms")
	RoomMembers = basic.Define[RoomMember, uuid.UUID, RoomMemberUpdate]("uu_members")
)

var uuSchema = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS uu_members`,
		`DROP TABLE IF EXISTS uu_rooms`,
		`CREATE TABLE uu_rooms (
			id uuid PRIMARY KEY,
			created_at timestamptz NOT NULL,
			kind text NOT NULL,
			name text)`,
		`CREATE TABLE uu_members (
			id uuid PRIMARY KEY,
			room_id uuid NOT NULL REFERENCES uu_rooms(id) ON DELETE CASCADE,
			role text,
			left_at timestamptz)`,
	},
	"mysql": {
		`DROP TABLE IF EXISTS uu_members`,
		`DROP TABLE IF EXISTS uu_rooms`,
		// MySQL has no uuid type, and uuid.UUID.Value() hands the driver the
		// 36-character string form rather than the 16 raw bytes — so the column
		// that matches what actually goes over the wire is char(36).
		`CREATE TABLE uu_rooms (
			id char(36) PRIMARY KEY,
			created_at datetime(6) NOT NULL,
			kind varchar(32) NOT NULL,
			name varchar(255))`,
		`CREATE TABLE uu_members (
			id char(36) PRIMARY KEY,
			room_id char(36) NOT NULL,
			role varchar(64),
			left_at datetime(6))`,
	},
}

var (
	uuOnce sync.Once
	uuErr  error
)

func uuSetup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	uuOnce.Do(func() {
		for _, tg := range egEngines() {
			for _, stmt := range uuSchema[tg.db] {
				if _, err := tg.src.Exec(ctx, stmt); err != nil {
					uuErr = errors.New(tg.db + ": " + err.Error())
					return
				}
			}
		}
	})
	if uuErr != nil {
		t.Fatalf("the uu tables were never built: %v", uuErr)
	}
	for _, tg := range egEngines() {
		for _, table := range []string{"uu_members", "uu_rooms"} {
			if _, err := tg.src.Exec(ctx, "DELETE FROM "+tg.src.Dialect().Quote(table)); err != nil {
				t.Fatalf("%s: wiping %s: %v", tg.db, table, err)
			}
		}
	}
}

// uuSeed writes one room with two members and returns their ids.
func uuSeed(t *testing.T, src crud.Source) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	rooms, members := Rooms.Bind(src), RoomMembers.Bind(src)

	name := "general"
	room := Room{
		ID:        uuid.Must(uuid.NewV7()),
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		Kind:      RoomGroup,
		Name:      &name,
	}
	if err := rooms.Save(ctx, &room); err != nil {
		t.Fatalf("saving a uuid-keyed row: %v", err)
	}

	var ids []uuid.UUID
	for _, role := range []string{"owner", "member"} {
		m := RoomMember{ID: uuid.Must(uuid.NewV7()), RoomID: room.ID, Role: &role}
		if err := members.Save(ctx, &m); err != nil {
			t.Fatalf("saving a member: %v", err)
		}
		ids = append(ids, m.ID)
	}
	return room.ID, ids
}

// The whole surface over a UUID key, on both engines.
func TestAUUIDPrimaryKeyWorksEverywhere(t *testing.T) {
	ctx := context.Background()
	uuSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			uuSetup(t)
			roomID, memberIDs := uuSeed(t, tg.src)
			rooms, members := Rooms.Bind(tg.src), RoomMembers.Bind(tg.src)

			t.Run("GetByID round trips the key", func(t *testing.T) {
				got, err := rooms.GetByID(ctx, roomID)
				if err != nil {
					t.Fatal(err)
				}
				if got.ID != roomID {
					t.Fatalf("id = %v, want %v", got.ID, roomID)
				}
				if got.Kind != RoomGroup {
					t.Fatalf("kind = %q: a defined string type did not survive", got.Kind)
				}
				if got.Name == nil || *got.Name != "general" {
					t.Fatalf("name = %v", got.Name)
				}
				if got.CreatedAt.IsZero() {
					t.Fatal("created_at came back zero")
				}
			})

			t.Run("a missing key is ErrNotFound, not a scan error", func(t *testing.T) {
				if _, err := rooms.GetByID(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, crud.ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			})

			t.Run("a UUID is bindable in a filter", func(t *testing.T) {
				n, err := members.Count(ctx, crud.Where(crud.Eq("RoomID", roomID)))
				if err != nil {
					t.Fatal(err)
				}
				if n != 2 {
					t.Fatalf("count = %d, want 2", n)
				}
				// IN over a list of UUIDs is a different binding path.
				n, err = members.Count(ctx, crud.Where(crud.InAny("ID", memberIDs)))
				if err != nil {
					t.Fatal(err)
				}
				if n != 2 {
					t.Fatalf("in-list count = %d, want 2", n)
				}
			})

			t.Run("a preload indexes by the UUID key", func(t *testing.T) {
				// This is the one that quietly returns nothing if the preloader
				// cannot use a [16]byte as a map key.
				got, err := rooms.GetAll(ctx, crud.Preload("Members"))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 {
					t.Fatalf("rooms = %d", len(got))
				}
				if len(got[0].Members) != 2 {
					t.Fatalf("members = %+v: the preload did not attach by UUID", got[0].Members)
				}
				// And back the other way, so belongs_to is covered too.
				back, err := members.GetAll(ctx, crud.Preload("Room"))
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range back {
					if m.Room == nil || m.Room.ID != roomID {
						t.Fatalf("member %v has room %+v", m.ID, m.Room)
					}
				}
			})

			t.Run("a nested filter walks a UUID join", func(t *testing.T) {
				n, err := rooms.Count(ctx, crud.Where(crud.Eq("Members.Role", "owner")))
				if err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("count = %d, want 1", n)
				}
			})

			t.Run("Update diffs a UUID-keyed row", func(t *testing.T) {
				renamed := "renamed"
				got, err := rooms.Update(ctx, roomID, RoomUpdate{Name: crud.Set(renamed)})
				if err != nil {
					t.Fatal(err)
				}
				if got.Name == nil || *got.Name != renamed {
					t.Fatalf("name = %v", got.Name)
				}
				if got.ID != roomID {
					t.Fatalf("the key changed: %v", got.ID)
				}
				// An explicit null on a nullable column, over a UUID key.
				got, err = rooms.Update(ctx, roomID, RoomUpdate{Name: crud.Null[string]()})
				if err != nil {
					t.Fatal(err)
				}
				if got.Name != nil {
					t.Fatalf("name = %v, want NULL", got.Name)
				}
			})

			t.Run("Delete takes UUID ids", func(t *testing.T) {
				n, err := members.Delete(ctx, memberIDs[0])
				if err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("deleted %d rows", n)
				}
				if _, err := members.GetByID(ctx, memberIDs[0]); !errors.Is(err, crud.ErrNotFound) {
					t.Fatalf("err = %v", err)
				}
			})
		})
	}
}

// The HTTP path: a client sends a UUID as a JSON string, and the compiler has to
// turn it into the column's Go type before it can be bound.
func TestTheDSLCoercesAUUIDFromTheWire(t *testing.T) {
	ctx := context.Background()
	uuSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			uuSetup(t)
			roomID, memberIDs := uuSeed(t, tg.src)
			members := RoomMembers.Bind(tg.src)

			var req query.Request
			body := `{"filter":{"roomId":"` + roomID.String() + `"},"sort":["role"],"limit":10}`
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatal(err)
			}
			opts, err := req.Compile(RoomMembers.Meta(), nil)
			if err != nil {
				t.Fatalf("compiling a uuid filter: %v", err)
			}
			page, err := members.Get(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 2 || len(page.Items) != 2 {
				t.Fatalf("page = total %d items %d", page.Total, len(page.Items))
			}

			// An "in" list of UUID strings, which is the shape a bulk filter takes.
			body = `{"filter":{"id":{"in":["` + memberIDs[0].String() + `","` + memberIDs[1].String() + `"]}}}`
			req = query.Request{}
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatal(err)
			}
			opts, err = req.Compile(RoomMembers.Meta(), nil)
			if err != nil {
				t.Fatalf("compiling a uuid in-list: %v", err)
			}
			if n, err := members.Count(ctx, opts...); err != nil || n != 2 {
				t.Fatalf("count = %d err = %v", n, err)
			}

			// A malformed one must be a query error naming the path, not a panic
			// and not a silently-zero UUID that matches nothing.
			req = query.Request{}
			if err := json.Unmarshal([]byte(`{"filter":{"roomId":"not-a-uuid"}}`), &req); err != nil {
				t.Fatal(err)
			}
			if _, err := req.Compile(RoomMembers.Meta(), nil); err == nil {
				t.Fatal("a malformed UUID compiled cleanly")
			}
		})
	}
}

// ent generates its UUIDs and timestamps in Go, and vv does not run ent's
// builders. The good news, pinned here because it is the difference between a
// clear error and a table full of zero keys: a model whose key is declared
// `noauto` and left unset is refused before any SQL runs. The bad news is the
// other half — nothing fills in a Go-side time default, so a NOT NULL timestamp
// column has to be set by the caller or defaulted by the database.
func TestAGoSideDefaultIsNotAppliedByVV(t *testing.T) {
	ctx := context.Background()
	uuSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			uuSetup(t)
			rooms := Rooms.Bind(tg.src)

			// No ID — exactly what ent's Default(uuid.NewV7) would have filled in.
			blank := Room{Kind: RoomDirect, CreatedAt: time.Now().UTC()}
			if err := rooms.Save(ctx, &blank); !errors.Is(err, crud.ErrMissingID) {
				t.Fatalf("err = %v, want ErrMissingID: an unset noauto key must be refused, "+
					"not written as the zero UUID", err)
			}
			if n, err := rooms.Count(ctx); err != nil || n != 0 {
				t.Fatalf("count = %d err = %v: the refused row was written anyway", n, err)
			}

			// The timestamp half: a key is set, the Go-side time default is not.
			// The column is NOT NULL with no database default, so the database is
			// the one that says no — which is the failure mode to know about.
			zeroTime := Room{ID: uuid.Must(uuid.NewV7()), Kind: RoomDirect}
			err := rooms.Save(ctx, &zeroTime)
			if err != nil {
				t.Logf("the database refused a zero created_at: %v", err)
				return
			}
			got, err := rooms.GetByID(ctx, zeroTime.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.CreatedAt.Year() > 1 {
				t.Fatalf("created_at = %v: something filled it in, so this test is stale", got.CreatedAt)
			}
			t.Logf("created_at was written as %v — a Go-side default is not applied", got.CreatedAt)
		})
	}
}

// A UUID column that is nullable is a *different* Go type again, and sql.Null
// is how a driver reports it. Covered here because a chat schema is full of
// optional foreign keys.
func TestANullableUUIDColumnRoundTrips(t *testing.T) {
	ctx := context.Background()
	uuSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			uuSetup(t)
			_, ids := uuSeed(t, tg.src)
			members := RoomMembers.Bind(tg.src)

			// left_at starts NULL.
			got, err := members.GetByID(ctx, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if !got.Left.IsNull() {
				t.Fatalf("left_at = %v, want NULL", got.Left)
			}

			when := time.Now().UTC().Truncate(time.Second)
			got, err = members.Update(ctx, ids[0], RoomMemberUpdate{Left: crud.Set(when)})
			if err != nil {
				t.Fatal(err)
			}
			if v, ok := got.Left.Get(); !ok || !v.Equal(when) {
				t.Fatalf("left_at = %v, want %v", got.Left, when)
			}
			if _, err := members.Update(ctx, ids[0], RoomMemberUpdate{Left: crud.Null[time.Time]()}); err != nil {
				t.Fatal(err)
			}
			got, err = members.GetByID(ctx, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if !got.Left.IsNull() {
				t.Fatalf("left_at = %v, want NULL again", got.Left)
			}
		})
	}
}
