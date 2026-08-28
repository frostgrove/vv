//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	"gorm.io/gorm"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/query"
	entpkg "github.com/frostgrove/vv/test/ent"
	entuser "github.com/frostgrove/vv/test/ent/user"
	"github.com/frostgrove/vv/test/entstore"
)

// These are the shapes the docs show: a DSL filter arrives over HTTP, a usecase
// opens one transaction, does something the ORM is better at, runs the
// repository with the compiled DSL options inside that same transaction, and
// commits. Everything is one atomic unit.

// ---------------------------------------------------------------------------
// gorm

type promoteResult struct {
	Matched  int64    `json:"matched"`
	Promoted []string `json:"promoted"`
}

type teamUsecase struct {
	db      *gorm.DB
	members *crud.Repo[Member, uint, MemberUpdate]
	cfg     *query.Config
}

// PromoteMembers is the whole pattern in one method.
func (uc teamUsecase) PromoteMembers(ctx context.Context, teamID uint, req *query.Request) (promoteResult, error) {
	// 1. The DSL is compiled and validated before anything opens.
	opts, err := req.Compile(uc.members.Meta(), uc.cfg)
	if err != nil {
		return promoteResult{}, err
	}

	var out promoteResult
	// 2. gorm owns the transaction.
	err = uc.db.Transaction(func(tx *gorm.DB) error {
		// 3. vv joins it — one line, and every repository call made with
		//    txCtx now runs on this transaction.
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))

		// 4. The client's filter, narrowed by something it cannot override.
		page, err := uc.members.Get(txCtx, append(opts, crud.Where(crud.Eq("TeamID", teamID)))...)
		if err != nil {
			return err
		}
		out.Matched = page.Total

		// 5. Something gorm is better at, in the same transaction.
		if err := tx.Model(&Team{}).Where("id = ?", teamID).
			Update("name", gorm.Expr("name || ' (promoted)'")).Error; err != nil {
			return err
		}

		// 6. Writes back through the repository, still the same transaction.
		for _, m := range page.Items {
			renamed := "Sr. " + m.Name
			updated, err := uc.members.Update(txCtx, m.ID, MemberUpdate{Name: &renamed})
			if err != nil {
				return err
			}
			out.Promoted = append(out.Promoted, updated.Name)
		}
		return nil
	})
	if err != nil {
		return promoteResult{}, err
	}
	// 7. Committed.
	return out, nil
}

func TestGormUsecaseDSLInsideTransaction(t *testing.T) {
	ctx := context.Background()
	db := gormDB(t)
	uc := teamUsecase{db: db, members: GormMembers.Bind(crudsql.Postgres(pgDB))}

	team := Team{Name: "core"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	young, senior := 22, 41
	if err := db.Create(&[]*Member{
		{TeamID: team.ID, Name: "Ann", Age: &senior},
		{TeamID: team.ID, Name: "Bob", Age: &young},
		{TeamID: team.ID, Name: "Cid", Age: &senior},
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Exactly what an HTTP client would post.
	var req query.Request
	if err := json.Unmarshal([]byte(`{"filter":{"age":{"gte":30}},"sort":["name"],"limit":50}`), &req); err != nil {
		t.Fatal(err)
	}

	got, err := uc.PromoteMembers(ctx, team.ID, &req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Matched != 2 || len(got.Promoted) != 2 {
		t.Fatalf("result = %+v", got)
	}
	if got.Promoted[0] != "Sr. Ann" || got.Promoted[1] != "Sr. Cid" {
		t.Fatalf("promoted = %v", got.Promoted)
	}

	// Both halves of the transaction landed.
	var reloaded Team
	if err := db.First(&reloaded, team.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "core (promoted)" {
		t.Fatalf("team = %q", reloaded.Name)
	}
	var bob Member
	if err := db.Where("name = ?", "Bob").First(&bob).Error; err != nil {
		t.Fatal(err)
	}
	if bob.Name != "Bob" {
		t.Fatalf("a member outside the filter was touched: %q", bob.Name)
	}
}

// A failure anywhere in the usecase takes the ORM's work and vv's with it.
func TestGormUsecaseRollsBackBothHalves(t *testing.T) {
	ctx := context.Background()
	db := gormDB(t)
	members := GormMembers.Bind(crudsql.Postgres(pgDB))

	team := Team{Name: "core"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Member{TeamID: team.ID, Name: "Ann"}).Error; err != nil {
		t.Fatal(err)
	}

	boom := errors.New("policy check failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))
		if err := tx.Model(&Team{}).Where("id = ?", team.ID).Update("name", "renamed").Error; err != nil {
			return err
		}
		name := "Renamed"
		if _, err := members.GetAll(txCtx); err != nil {
			return err
		}
		if err := members.Save(txCtx, &Member{TeamID: team.ID, Name: name}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}

	var reloaded Team
	if err := db.First(&reloaded, team.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "core" {
		t.Fatalf("gorm's write survived the rollback: %q", reloaded.Name)
	}
	if n, _ := members.Count(ctx); n != 1 {
		t.Fatalf("github.com/frostgrove/vv's write survived the rollback: count = %d", n)
	}
}

// ---------------------------------------------------------------------------
// ent

type userUsecase struct {
	client *entpkg.Client
	users  *crud.Repo[entpkg.User, int64, entstore.UserUpdate]
	cfg    *query.Config
}

type deactivateResult struct {
	Matched     int64    `json:"matched"`
	Deactivated []string `json:"deactivated"`
}

func (uc userUsecase) DeactivateUsers(ctx context.Context, tenantID int64, req *query.Request) (deactivateResult, error) {
	opts, err := req.Compile(uc.users.Meta(), uc.cfg)
	if err != nil {
		return deactivateResult{}, err
	}

	var out deactivateResult
	// ent owns the transaction, so ent's hooks and privacy rules still run.
	tx, err := uc.client.Tx(ctx)
	if err != nil {
		return deactivateResult{}, err
	}
	defer tx.Rollback()

	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))

	page, err := uc.users.Get(txCtx, append(opts, crud.Where(crud.Eq("TenantID", tenantID)))...)
	if err != nil {
		return deactivateResult{}, err
	}
	out.Matched = page.Total

	// ent's builder, same transaction: a bulk update expressed the ent way.
	ids := make([]int64, 0, len(page.Items))
	for _, u := range page.Items {
		ids = append(ids, u.ID)
		out.Deactivated = append(out.Deactivated, u.Email)
	}
	if len(ids) > 0 {
		if _, err := tx.User.Update().Where(entuser.IDIn(ids...)).SetActive(false).Save(ctx); err != nil {
			return deactivateResult{}, err
		}
	}

	// …and a repository write, still the same transaction.
	for _, id := range ids {
		name := "deactivated"
		if _, err := uc.users.Update(txCtx, id, entstore.UserUpdate{Name: &name}); err != nil {
			return deactivateResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return deactivateResult{}, err
	}
	return out, nil
}

func TestEntUsecaseDSLInsideTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)

	client := entClient(pgDB, dialect.Postgres)
	uc := userUsecase{client: client, users: EntUsers.Bind(crudsql.Postgres(pgDB))}

	for i, name := range []string{"Ann", "Bob", "Cid"} {
		if _, err := client.User.Create().
			SetTenantID(1).SetEmail(name + "@x.io").SetName(name).SetAge(28 + i*5).SetActive(true).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.User.Create().
		SetTenantID(2).SetEmail("other@x.io").SetName("Other").SetAge(50).SetActive(true).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	var req query.Request
	if err := json.Unmarshal([]byte(`{"filter":{"age":{"gte":33}},"sort":["email"],"limit":50}`), &req); err != nil {
		t.Fatal(err)
	}

	got, err := uc.DeactivateUsers(ctx, 1, &req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Matched != 2 || len(got.Deactivated) != 2 {
		t.Fatalf("result = %+v", got)
	}

	// ent's bulk update and vv's per-row update both committed.
	still, err := client.User.Query().Where(entuser.ActiveEQ(true)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(still) != 2 {
		t.Fatalf("%d users still active, want 2 (Ann and the other tenant)", len(still))
	}
	renamed, err := client.User.Query().Where(entuser.NameEQ("deactivated")).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if renamed != 2 {
		t.Fatalf("%d renamed, want 2", renamed)
	}
	// The other tenant was never in scope.
	other, err := client.User.Query().Where(entuser.TenantIDEQ(2)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !other.Active || other.Name != "Other" {
		t.Fatalf("another tenant's row was touched: %+v", other)
	}
}
