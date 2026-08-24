//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shardit-io/qq/crud"
	"github.com/shardit-io/qq/repo/decorators/security"
	"github.com/shardit-io/qq/repo/decorators/specs"
)

// Target is one driver bound to one database.
type Target struct {
	Name   string
	DB     string // "postgres" or "mysql"
	Source crud.Source
}

func ptr[T any](v T) *T { return &v }

func (tg Target) reset(t *testing.T) {
	t.Helper()
	if _, err := tg.Source.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

// RunSuite is the conformance suite. Every driver runs exactly this.
func RunSuite(t *testing.T, tg Target) {
	ctx := context.Background()
	repo := Users.Bind(tg.Source)

	t.Run("SaveAssignsGeneratedKeyAndDefaults", func(t *testing.T) {
		tg.reset(t)
		u := User{TenantID: 1, Email: "ann@x.io", Name: "Ann", Age: crud.Set(31), Active: true}

		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}
		if u.ID == 0 {
			t.Fatal("the generated key was not read back")
		}
		if u.CreatedAt.IsZero() {
			t.Fatal("the database default for created_at was not read back")
		}
		if time.Since(u.CreatedAt) > time.Minute {
			t.Fatalf("created_at = %v, looks wrong", u.CreatedAt)
		}

		got, err := repo.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Email != "ann@x.io" || got.Name != "Ann" || !got.Active || got.TenantID != 1 {
			t.Fatalf("round trip = %+v", got)
		}
		if age, ok := got.Age.Get(); !ok || age != 31 {
			t.Fatalf("age = %v", got.Age)
		}
	})

	t.Run("NullableColumnRoundTrip", func(t *testing.T) {
		tg.reset(t)
		u := User{TenantID: 1, Email: "no-age@x.io", Name: "NoAge"}
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Age.IsNull() {
			t.Fatalf("age = %v, want NULL", got.Age)
		}
	})

	t.Run("SaveWithKeyUpserts", func(t *testing.T) {
		tg.reset(t)
		u := User{TenantID: 1, Email: "up@x.io", Name: "Before", Active: true}
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}
		created := u.CreatedAt

		u.Name = "After"
		u.TenantID = 99 // immutable: must not be written on conflict
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "After" {
			t.Fatalf("name = %q, want After", got.Name)
		}
		if got.TenantID != 1 {
			t.Fatalf("tenant_id = %d: an immutable column was overwritten", got.TenantID)
		}
		if !got.CreatedAt.Equal(created) {
			t.Fatalf("created_at changed: %v -> %v", created, got.CreatedAt)
		}
		if n, err := repo.Count(ctx); err != nil || n != 1 {
			t.Fatalf("count = %d err = %v: the upsert inserted a second row", n, err)
		}
	})

	t.Run("GetByIDMissing", func(t *testing.T) {
		tg.reset(t)
		if _, err := repo.GetByID(ctx, 999999); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateWritesOnlyWhatChanged", func(t *testing.T) {
		tg.reset(t)
		u := User{TenantID: 1, Email: "u@x.io", Name: "Ann", Age: crud.Set(31), Active: true}
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}

		got, err := repo.Update(ctx, u.ID, UserUpdate{
			Email: ptr("u@x.io"), // same value — no-op
			Name:  ptr("Anna"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Anna" || got.Email != "u@x.io" {
			t.Fatalf("update = %+v", got)
		}
		if age, ok := got.Age.Get(); !ok || age != 31 {
			t.Fatalf("an undefined DTO field changed the row: age = %v", got.Age)
		}
		if !got.Active {
			t.Fatal("an undefined DTO field changed the row: active")
		}
	})

	t.Run("UpdateTellsUndefinedFromNull", func(t *testing.T) {
		tg.reset(t)
		u := User{TenantID: 1, Email: "n@x.io", Name: "N", Age: crud.Set(20)}
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}

		// Undefined: age survives.
		got, err := repo.Update(ctx, u.ID, UserUpdate{Name: ptr("N2")})
		if err != nil {
			t.Fatal(err)
		}
		if age, ok := got.Age.Get(); !ok || age != 20 {
			t.Fatalf("undefined Opt cleared the column: %v", got.Age)
		}

		// Explicit null: age is cleared.
		got, err = repo.Update(ctx, u.ID, UserUpdate{Age: crud.Null[int]()})
		if err != nil {
			t.Fatal(err)
		}
		if !got.Age.IsNull() {
			t.Fatalf("age = %v, want NULL", got.Age)
		}

		// And back to a value.
		got, err = repo.Update(ctx, u.ID, UserUpdate{Age: crud.Set(21)})
		if err != nil {
			t.Fatal(err)
		}
		if age, _ := got.Age.Get(); age != 21 {
			t.Fatalf("age = %v", got.Age)
		}
	})

	t.Run("UpdateWithNothingToDo", func(t *testing.T) {
		tg.reset(t)
		u := User{TenantID: 1, Email: "same@x.io", Name: "Same", Active: true}
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Update(ctx, u.ID, UserUpdate{Name: ptr("Same"), Active: ptr(true)})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Same" {
			t.Fatalf("model = %+v", got)
		}
	})

	t.Run("UpdateMissing", func(t *testing.T) {
		tg.reset(t)
		if _, err := repo.Update(ctx, 424242, UserUpdate{Name: ptr("x")}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateAllWritesEveryMatchingRowInOneStatement", func(t *testing.T) {
		tg.reset(t)
		seed(t, repo, 6)

		// The filtered write: one statement for "everybody over 22".
		n, err := repo.UpdateAll(ctx, UserUpdate{Active: ptr(false)}, crud.Where(crud.Gt("Age", 22)))
		if err != nil {
			t.Fatal(err)
		}
		if n != 3 {
			t.Fatalf("rows affected = %d, want the 3 rows over 22", n)
		}
		off, err := repo.Count(ctx, crud.Where(crud.Eq("Active", false)))
		if err != nil {
			t.Fatal(err)
		}
		if off != 3 {
			t.Fatalf("%d rows are inactive, want 3: the filter reached rows it should not have", off)
		}

		// Undefined stays undefined, and a null Opt still means NULL — the two
		// rules Update lives by, on a write with no row to diff against.
		if _, err := repo.UpdateAll(ctx, UserUpdate{Age: crud.Null[int]()}, crud.Where(crud.Eq("Email", "user-00@x.io"))); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetAll(ctx, crud.Where(crud.Eq("Email", "user-00@x.io")))
		if err != nil {
			t.Fatal(err)
		}
		if !got[0].Age.IsNull() {
			t.Fatalf("age = %v, want NULL", got[0].Age)
		}
		if got[0].Name != "user 0" {
			t.Fatalf("an undefined DTO field changed the row: name = %q", got[0].Name)
		}

		// A DTO that defines nothing is not a request to rewrite the table.
		if n, err := repo.UpdateAll(ctx, UserUpdate{}); err != nil || n != 0 {
			t.Fatalf("n = %d err = %v: an empty DTO must write nothing", n, err)
		}

		// Matching nothing is not an error; it is a count of zero.
		if n, err := repo.UpdateAll(ctx, UserUpdate{Name: ptr("x")}, crud.Where(crud.Eq("Email", "nobody@x.io"))); err != nil || n != 0 {
			t.Fatalf("n = %d err = %v", n, err)
		}

		// The engines disagree about what "affected" counts — PostgreSQL reports
		// the rows it matched, MySQL the rows whose values actually changed — so
		// what is pinned here is the row count in the table, not the number.
		if _, err := repo.UpdateAll(ctx, UserUpdate{Name: ptr("all the same")}); err != nil {
			t.Fatal(err)
		}
		same, err := repo.Count(ctx, crud.Where(crud.Eq("Name", "all the same")))
		if err != nil {
			t.Fatal(err)
		}
		if same != 6 {
			t.Fatalf("%d of 6 rows were renamed by an unfiltered UpdateAll", same)
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		tg.reset(t)
		seed(t, repo, 25)

		page, err := repo.Get(ctx, crud.Page(2), crud.Limit(10), crud.OrderBy(crud.Asc("Email")))
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 10 || page.Total != 25 || page.TotalPages != 3 || page.Page != 2 {
			t.Fatalf("page = %+v", pageOf(page))
		}
		if !page.HasNext || !page.HasPrev {
			t.Fatalf("flags = %+v", pageOf(page))
		}
		if page.Items[0].Email != "user-10@x.io" {
			t.Fatalf("first item of page 2 = %s", page.Items[0].Email)
		}

		last, err := repo.Get(ctx, crud.Page(3), crud.Limit(10))
		if err != nil {
			t.Fatal(err)
		}
		if len(last.Items) != 5 || last.HasNext || !last.HasPrev {
			t.Fatalf("last page = %+v", pageOf(last))
		}

		// SkipTotal answers HasNext without a COUNT.
		probe, err := repo.Get(ctx, crud.Page(1), crud.Limit(10), crud.SkipTotal())
		if err != nil {
			t.Fatal(err)
		}
		if len(probe.Items) != 10 || !probe.HasNext {
			t.Fatalf("probe = %+v", pageOf(probe))
		}

		all, err := repo.GetAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 25 {
			t.Fatalf("GetAll returned %d rows", len(all))
		}
	})

	t.Run("Predicates", func(t *testing.T) {
		tg.reset(t)
		seed(t, repo, 10)
		if _, err := repo.Update(ctx, mustID(t, repo, "user-00@x.io"), UserUpdate{Age: crud.Null[int]()}); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name string
			pred crud.Predicate
			want int
		}{
			{"eq", crud.Eq("Name", "user 3"), 1},
			{"ne", crud.Ne("Name", "user 3"), 9},
			{"gt", crud.Gt("Age", 25), 4},
			{"between", crud.Between("Age", 22, 24), 3},
			{"in", crud.InAny("Email", []string{"user-01@x.io", "user-02@x.io"}), 2},
			{"not in", crud.NotInAny("Email", []string{"user-01@x.io"}), 9},
			{"is null", crud.IsNull("Age"), 1},
			{"is not null", crud.IsNotNull("Age"), 9},
			{"contains", crud.Contains("Email", "-0"), 10},
			{"starts with", crud.StartsWith("Email", "user-0"), 10},
			{"like ignore case", crud.LikeIgnoreCase("Name", "USER 4"), 1},
			{"and", crud.And(crud.Gt("Age", 22), crud.Lt("Age", 25)), 2},
			{"or", crud.Or(crud.Eq("Name", "user 1"), crud.Eq("Name", "user 2")), 2},
			{"not", crud.Not(crud.Eq("Name", "user 1")), 9},
			{"eq nil is a null test", crud.Eq("Age", nil), 1},
			{"empty in", crud.InAny("Email", []string{}), 0},
			{"true", crud.True(), 10},
			{"raw", crud.Raw("age > ?", 25), 4},

			// The rest of the constructors. They were unit-render-only, which
			// proves the statement and not the answer: a LIKE pattern that no
			// engine interprets the way the renderer assumed, or a column-to-
			// column comparison that one dialect will not quote, produces
			// perfectly good SQL and the wrong rows.
			{"lte", crud.Lte("Age", 22), 2}, // 21 and 22; user-00's age was nulled above
			{"gte", crud.Gte("Age", 27), 3},
			{"like", crud.Like("Email", "user-0_@x.io"), 10},
			{"not like", crud.NotLike("Email", "%-01@%"), 9},
			{"in, variadic", crud.In("Name", "user 1", "user 2", "nobody"), 2},
			{"not in, variadic", crud.NotIn("Name", "user 1", "user 2"), 8},
			{"in, one value", crud.In("Name", "user 1"), 1},
			{"in, nothing at all", crud.In("Name"), 0},
			{"false", crud.False(), 0},
			{"false ors away", crud.Or(crud.False(), crud.Eq("Name", "user 1")), 1},
			{"a column compared with another column", crud.EqField("Name", "Name"), 10},
		} {
			t.Run(tc.name, func(t *testing.T) {
				n, err := repo.Count(ctx, crud.Where(tc.pred))
				if err != nil {
					t.Fatal(err)
				}
				if int(n) != tc.want {
					t.Fatalf("count = %d, want %d", n, tc.want)
				}
			})
		}
	})

	t.Run("Sorting", func(t *testing.T) {
		tg.reset(t)
		seed(t, repo, 5)
		desc, err := repo.GetAll(ctx, crud.OrderBy(crud.Desc("Email")))
		if err != nil {
			t.Fatal(err)
		}
		if desc[0].Email != "user-04@x.io" {
			t.Fatalf("first = %s", desc[0].Email)
		}
	})

	t.Run("CountAndExists", func(t *testing.T) {
		tg.reset(t)
		seed(t, repo, 3)
		if n, err := repo.Count(ctx); err != nil || n != 3 {
			t.Fatalf("count = %d err = %v", n, err)
		}
		ok, err := repo.Exists(ctx, crud.Where(crud.Eq("Email", "user-01@x.io")))
		if err != nil || !ok {
			t.Fatalf("exists = %v err = %v", ok, err)
		}
		ok, err = repo.Exists(ctx, crud.Where(crud.Eq("Email", "nobody@x.io")))
		if err != nil || ok {
			t.Fatalf("exists = %v err = %v", ok, err)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		tg.reset(t)
		users := seed(t, repo, 5)

		n, err := repo.Delete(ctx, users[0].ID, users[1].ID)
		if err != nil || n != 2 {
			t.Fatalf("n = %d err = %v", n, err)
		}
		if _, err := repo.GetByID(ctx, users[0].ID); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}

		// Deleting rows that are already gone is not an error.
		if n, err := repo.Delete(ctx, users[0].ID); err != nil || n != 0 {
			t.Fatalf("n = %d err = %v", n, err)
		}

		if n, err := repo.DeleteAll(ctx, crud.Where(crud.Gte("Age", 23))); err != nil || n != 2 {
			t.Fatalf("n = %d err = %v", n, err)
		}
		if n, err := repo.DeleteAll(ctx); err != nil || n != 1 {
			t.Fatalf("n = %d err = %v", n, err)
		}
		if n, err := repo.Count(ctx); err != nil || n != 0 {
			t.Fatalf("count = %d err = %v", n, err)
		}
	})

	t.Run("Specifications", func(t *testing.T) {
		tg.reset(t)
		seed(t, repo, 10)
		sp := specs.Executor(repo)

		adults := specs.Where(User_.Age.Gte(25)).And(User_.Active.Eq(true))
		found, err := sp.FindAll(ctx, adults, crud.OrderBy(User_.Age.Desc()))
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 5 {
			t.Fatalf("found %d, want 5", len(found))
		}
		if age, _ := found[0].Age.Get(); age != 29 {
			t.Fatalf("sorted wrong: %v", found[0].Age)
		}

		one, err := sp.FindOne(ctx, User_.Email.Eq("user-03@x.io"))
		if err != nil {
			t.Fatal(err)
		}
		if one.Name != "user 3" {
			t.Fatalf("found %+v", one)
		}
		if _, err := sp.FindOne(ctx, User_.Active.Eq(true)); !errors.Is(err, specs.ErrNotUnique) {
			t.Fatalf("err = %v, want ErrNotUnique", err)
		}

		// The literal criteria-builder form has to agree with the metamodel one.
		byBuilder := specs.Of[User](func(r specs.Root[User], cb specs.Builder) crud.Predicate {
			return cb.And(
				cb.GreaterThanOrEqualTo(r.Get("Age"), 25),
				cb.Equal(r.Get("Active"), true),
			)
		})
		n1, err := sp.CountBy(ctx, adults)
		if err != nil {
			t.Fatal(err)
		}
		n2, err := sp.CountBy(ctx, byBuilder)
		if err != nil {
			t.Fatal(err)
		}
		if n1 != n2 || n1 != 5 {
			t.Fatalf("criteria builder and metamodel disagree: %d vs %d", n1, n2)
		}

		page, err := sp.FindPage(ctx, User_.Name.Contains("user"), crud.Page(1), crud.Limit(4))
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 10 || page.TotalPages != 3 || len(page.Items) != 4 {
			t.Fatalf("page = %+v", pageOf(page))
		}

		if ok, err := sp.ExistsBy(ctx, User_.Email.Eq("user-09@x.io")); err != nil || !ok {
			t.Fatalf("exists = %v err = %v", ok, err)
		}

		// The rest of the metamodel and the rest of the criteria builder, each
		// against the same ten rows. Rendering them is not the same as running
		// them: a pattern the engine reads differently, or a column-to-column
		// comparison one dialect will not quote, is perfectly good SQL and the
		// wrong answer.
		for _, tc := range []struct {
			name string
			spec specs.Specification[User]
			want int64
		}{
			{"Str.Like", User_.Email.Like("user-0_@x.io"), 10},
			{"Str.NotLike", User_.Email.NotLike("%-01@%"), 9},
			{"Str.LikeIgnoreCase", User_.Name.LikeIgnoreCase("USER 4"), 1},
			{"Str.StartsWith", User_.Name.StartsWith("user"), 10},
			{"Str.EndsWith", User_.Email.EndsWith("@x.io"), 10},
			{"Ord.Gt", User_.Age.Gt(27), 2},
			{"Ord.Lt", User_.Age.Lt(21), 1},
			{"Ord.Lte", User_.Age.Lte(21), 2},
			{"Ord.Between", User_.Age.Between(22, 24), 3},
			{"Attr.NotNull", User_.Age.NotNull(), 10},
			{"Attr.IsNull", User_.Age.IsNull(), 0},
			{"Attr.In", User_.Name.In("user 1", "user 2"), 2},
			{"Attr.NotIn", User_.Name.NotIn("user 1"), 9},
			{"Attr.Ne", User_.Active.Ne(true), 0},
			{"Cmp.Lt on a timestamp", User_.CreatedAt.Lt(time.Now().Add(time.Hour)), 10},

			{"cb.NotEqual", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.NotEqual(r.Get("Name"), "user 1")
			}), 9},
			{"cb.GreaterThan and cb.LessThan", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.And(cb.GreaterThan(r.Get("Age"), 21), cb.LessThan(r.Get("Age"), 25))
			}), 3},
			{"cb.LessThanOrEqualTo", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.LessThanOrEqualTo(r.Get("Age"), 21)
			}), 2},
			{"cb.Between", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.Between(r.Get("Age"), 22, 24)
			}), 3},
			{"cb.Like and cb.NotLike", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.And(cb.Like(r.Get("Email"), "user-%"), cb.NotLike(r.Get("Email"), "%-01@%"))
			}), 9},
			{"cb.LikeIgnoreCase", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.LikeIgnoreCase(r.Get("Name"), "USER 4")
			}), 1},
			{"cb.IsNull and cb.IsNotNull", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.Or(cb.IsNull(r.Get("Age")), cb.IsNotNull(r.Get("Age")))
			}), 10},
			{"cb.In and cb.NotIn", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.And(cb.In(r.Get("Name"), "user 1", "user 2"), cb.NotIn(r.Get("Name"), "user 2"))
			}), 1},
			{"cb.EqualTo compares two columns", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.EqualTo(r.Get("Name"), r.Get("Name"))
			}), 10},
			{"cb.Conjunction is everything", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.Conjunction()
			}), 10},
			{"cb.Disjunction is nothing", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.Disjunction()
			}), 0},
			{"cb.Not", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.Not(cb.Equal(r.Get("Name"), "user 1"))
			}), 9},
			{"cb.Raw", byCriteria(func(r specs.Root[User], cb specs.Builder) crud.Predicate {
				return cb.Raw("age >= ?", 25)
			}), 5},
		} {
			t.Run(tc.name, func(t *testing.T) {
				n, err := sp.CountBy(ctx, tc.spec)
				if err != nil {
					t.Fatal(err)
				}
				if n != tc.want {
					t.Fatalf("count = %d, want %d", n, tc.want)
				}
			})
		}

		// Attr.Asc is a sort term, so its proof is the order that comes back.
		byAge, err := sp.FindAll(ctx, User_.Active.Eq(true), crud.SortBy(User_.Age.Asc()))
		if err != nil {
			t.Fatal(err)
		}
		if age, _ := byAge[0].Age.Get(); age != 20 {
			t.Fatalf("the first row is %v, want the youngest", byAge[0].Age)
		}

		// UpdateBy is the filtered write, addressed by specification.
		n, err := sp.UpdateBy(ctx, User_.Age.Gte(28), UserUpdate{Name: ptr("senior")})
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("n = %d, want the 2 rows aged 28 and over", n)
		}
		if seniors, err := sp.CountBy(ctx, User_.Name.Eq("senior")); err != nil || seniors != 2 {
			t.Fatalf("count = %d err = %v", seniors, err)
		}
		// And an empty specification is refused rather than applied to the table.
		if _, err := sp.UpdateBy(ctx, specs.Where[User](nil), UserUpdate{Name: ptr("everyone")}); !errors.Is(err, specs.ErrUnboundedUpdate) {
			t.Fatalf("err = %v, want ErrUnboundedUpdate", err)
		}

		if n, err := sp.DeleteBy(ctx, User_.Age.Lt(22)); err != nil || n != 2 {
			t.Fatalf("n = %d err = %v", n, err)
		}
	})

	t.Run("SecurityGate", func(t *testing.T) {
		tg.reset(t)
		gated := Users.Bind(tg.Source, security.Gate(security.ScopeField[User, int64]("TenantID", tenantOf)))

		// Two tenants, three rows.
		for _, u := range []User{
			{TenantID: 1, Email: "t1-a@x.io", Name: "A", Active: true},
			{TenantID: 1, Email: "t1-b@x.io", Name: "B", Active: true},
			{TenantID: 2, Email: "t2-a@x.io", Name: "C", Active: true},
		} {
			if err := repo.Save(ctx, &u); err != nil {
				t.Fatal(err)
			}
		}
		mine := withTenant(ctx, 1)
		theirs := withTenant(ctx, 2)

		got, err := gated.GetAll(mine)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("tenant 1 sees %d rows, want 2", len(got))
		}

		other, err := repo.GetAll(theirs, crud.Where(crud.Eq("TenantID", 2)))
		if err != nil {
			t.Fatal(err)
		}
		foreignID := other[0].ID

		if _, err := gated.GetByID(mine, foreignID); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("err = %v: a foreign row must look missing", err)
		}
		if _, err := gated.Update(mine, foreignID, UserUpdate{Name: ptr("hacked")}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("err = %v: a foreign row must not be updatable", err)
		}
		if n, err := gated.Delete(mine, foreignID); err != nil || n != 0 {
			t.Fatalf("n = %d err = %v: a foreign row must not be deletable", n, err)
		}
		// The row is untouched.
		if _, err := repo.GetByID(ctx, foreignID); err != nil {
			t.Fatalf("the foreign row is gone: %v", err)
		}

		// Writing into another tenant is refused.
		sneaky := User{TenantID: 2, Email: "sneak@x.io", Name: "S"}
		if err := gated.Save(mine, &sneaky); !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		// Writing into my own is fine.
		ok := User{TenantID: 1, Email: "ok@x.io", Name: "OK"}
		if err := gated.Save(mine, &ok); err != nil {
			t.Fatal(err)
		}
		if n, err := gated.Count(mine); err != nil || n != 3 {
			t.Fatalf("count = %d err = %v", n, err)
		}
		if n, err := gated.Count(theirs); err != nil || n != 1 {
			t.Fatalf("count = %d err = %v", n, err)
		}
	})

	t.Run("TransactionCommits", func(t *testing.T) {
		tg.reset(t)
		err := repo.Tx(ctx, func(ctx context.Context) error {
			for i := range 3 {
				u := User{TenantID: 1, Email: fmt.Sprintf("tx-%d@x.io", i), Name: "tx"}
				if err := repo.Save(ctx, &u); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if n, err := repo.Count(ctx); err != nil || n != 3 {
			t.Fatalf("count = %d err = %v", n, err)
		}
	})

	t.Run("TransactionRollsBack", func(t *testing.T) {
		tg.reset(t)
		boom := errors.New("boom")
		err := repo.Tx(ctx, func(ctx context.Context) error {
			u := User{TenantID: 1, Email: "rollback@x.io", Name: "gone"}
			if err := repo.Save(ctx, &u); err != nil {
				return err
			}
			if n, err := repo.Count(ctx); err != nil || n != 1 {
				t.Errorf("inside the transaction count = %d err = %v", n, err)
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if n, err := repo.Count(ctx); err != nil || n != 0 {
			t.Fatalf("the transaction was not rolled back: count = %d err = %v", n, err)
		}
	})

	t.Run("NestedTransactionJoins", func(t *testing.T) {
		tg.reset(t)
		err := repo.Tx(ctx, func(ctx context.Context) error {
			// A second Tx on a context that already carries one must join it
			// rather than open a competing transaction.
			return repo.Tx(ctx, func(ctx context.Context) error {
				u := User{TenantID: 1, Email: "nested@x.io", Name: "n"}
				return repo.Save(ctx, &u)
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		if n, err := repo.Count(ctx); err != nil || n != 1 {
			t.Fatalf("count = %d err = %v", n, err)
		}
	})
}

// byCriteria is specs.Of with the type parameter filled in, so the table above
// reads as a list of criteria expressions rather than of generic instantiations.
func byCriteria(f func(specs.Root[User], specs.Builder) crud.Predicate) specs.Specification[User] {
	return specs.Of[User](f)
}

// seed inserts n users with predictable emails, names and ages 20..20+n.
func seed(t *testing.T, repo crud.Repo[User, int64, UserUpdate], n int) []User {
	t.Helper()
	ctx := context.Background()
	out := make([]User, 0, n)
	for i := range n {
		u := User{
			TenantID: 1,
			Email:    fmt.Sprintf("user-%02d@x.io", i),
			Name:     fmt.Sprintf("user %d", i),
			Age:      crud.Set(20 + i),
			Active:   true,
		}
		if err := repo.Save(ctx, &u); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		out = append(out, u)
	}
	return out
}

func mustID(t *testing.T, repo crud.Repo[User, int64, UserUpdate], email string) int64 {
	t.Helper()
	got, err := repo.GetAll(context.Background(), crud.Where(crud.Eq("Email", email)))
	if err != nil || len(got) != 1 {
		t.Fatalf("looking up %s: %d rows, err = %v", email, len(got), err)
	}
	return got[0].ID
}

// pageOf renders the pager without the items, for readable failures.
func pageOf(p crud.PaginatedResponse[User]) string {
	return fmt.Sprintf("items=%d page=%d limit=%d total=%d pages=%d next=%v prev=%v",
		len(p.Items), p.Page, p.Limit, p.Total, p.TotalPages, p.HasNext, p.HasPrev)
}

type tenantKey struct{}

func withTenant(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, tenantKey{}, id)
}

func tenantOf(ctx context.Context) (any, error) {
	t, ok := ctx.Value(tenantKey{}).(int64)
	if !ok {
		return nil, security.Denied(security.Read, "no tenant in context")
	}
	return t, nil
}
