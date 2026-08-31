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

type promoteResult struct {
	Matched  int64    `json:"matched"`
	Promoted []string `json:"promoted"`
}

type teamUsecase struct {
	database *gorm.DB
	source   crud.Source
	members  *crud.Repo[Member, uint, MemberUpdate]
	config   *query.Config
}

func (this teamUsecase) PromoteMembers(ctx context.Context, teamID uint, request *query.Request) (promoteResult, error) {
	options, err := request.Compile(this.members.Meta(), this.config)
	if err != nil {
		return promoteResult{}, err
	}

	var out promoteResult

	err = this.database.Transaction(func(tx *gorm.DB) error {
		txCtx := crud.BindExecutor(ctx, this.source, crudsql.From(tx.Statement.ConnPool))

		page, err := this.members.Get(txCtx, append(options, crud.Where(crud.Eq("TeamID", teamID)))...)
		if err != nil {
			return err
		}
		out.Matched = page.Total

		if err := tx.Model(&Team{}).Where("id = ?", teamID).
			Update("name", gorm.Expr("name || ' (promoted)'")).Error; err != nil {
			return err
		}

		for _, m := range page.Items {
			renamed := "Sr. " + m.Name
			updated, err := this.members.Update(txCtx, m.ID, MemberUpdate{Name: &renamed})
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

	return out, nil
}

func TestGormUsecaseDSLInsideTransaction(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	source := crudsql.Postgres(pgDB)
	uc := teamUsecase{database: database, source: source, members: GormMembers.Bind(source)}

	team := Team{Name: "core"}
	if err := database.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	young, senior := 22, 41
	if err := database.Create(&[]*Member{
		{TeamID: team.ID, Name: "Ann", Age: &senior},
		{TeamID: team.ID, Name: "Bob", Age: &young},
		{TeamID: team.ID, Name: "Cid", Age: &senior},
	}).Error; err != nil {
		t.Fatal(err)
	}

	var request query.Request
	if err := json.Unmarshal([]byte(`{"filter":{"age":{"gte":30}},"sort":["name"],"limit":50}`), &request); err != nil {
		t.Fatal(err)
	}

	got, err := uc.PromoteMembers(ctx, team.ID, &request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Matched != 2 || len(got.Promoted) != 2 {
		t.Fatalf("result = %+v", got)
	}
	if got.Promoted[0] != "Sr. Ann" || got.Promoted[1] != "Sr. Cid" {
		t.Fatalf("promoted = %v", got.Promoted)
	}

	var reloaded Team
	if err := database.First(&reloaded, team.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "core (promoted)" {
		t.Fatalf("team = %q", reloaded.Name)
	}
	var bob Member
	if err := database.Where("name = ?", "Bob").First(&bob).Error; err != nil {
		t.Fatal(err)
	}
	if bob.Name != "Bob" {
		t.Fatalf("a member outside the filter was touched: %q", bob.Name)
	}
}

func TestGormUsecaseRollsBackBothHalves(t *testing.T) {
	ctx := context.Background()
	database := gormDB(t)
	source := crudsql.Postgres(pgDB)
	members := GormMembers.Bind(source)

	team := Team{Name: "core"}
	if err := database.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&Member{TeamID: team.ID, Name: "Ann"}).Error; err != nil {
		t.Fatal(err)
	}

	boom := errors.New("policy check failed")
	err := database.Transaction(func(tx *gorm.DB) error {
		txCtx := source.BindExecutor(ctx, tx.Statement.ConnPool)
		if err := tx.Model(&Team{}).Where("id = ?", team.ID).Update("name", "renamed").Error; err != nil {
			return err
		}
		name := "Renamed"
		if _, err := members.GetAll(txCtx); err != nil {
			return err
		}
		if _, err := members.Save(txCtx, &Member{TeamID: team.ID, Name: name}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}

	var reloaded Team
	if err := database.First(&reloaded, team.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "core" {
		t.Fatalf("gorm's write survived the rollback: %q", reloaded.Name)
	}
	if n, _ := members.Count(ctx); n != 1 {
		t.Fatalf("github.com/frostgrove/vv's write survived the rollback: count = %d", n)
	}
}

type userUsecase struct {
	client *entpkg.Client
	source crud.Source
	users  *crud.Repo[entpkg.User, int64, entstore.UserUpdate]
	config *query.Config
}

type deactivateResult struct {
	Matched     int64    `json:"matched"`
	Deactivated []string `json:"deactivated"`
}

func (this userUsecase) DeactivateUsers(ctx context.Context, tenantID int64, request *query.Request) (deactivateResult, error) {
	options, err := request.Compile(this.users.Meta(), this.config)
	if err != nil {
		return deactivateResult{}, err
	}

	var out deactivateResult

	tx, err := this.client.Tx(ctx)
	if err != nil {
		return deactivateResult{}, err
	}
	defer tx.Rollback()

	txCtx := crud.BindExecutor(ctx, this.source, crudsql.From(tx))

	page, err := this.users.Get(txCtx, append(options, crud.Where(crud.Eq("TenantID", tenantID)))...)
	if err != nil {
		return deactivateResult{}, err
	}
	out.Matched = page.Total

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

	for _, id := range ids {
		name := "deactivated"
		if _, err := this.users.Update(txCtx, id, entstore.UserUpdate{Name: &name}); err != nil {
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
	source := crudsql.Postgres(pgDB)
	uc := userUsecase{client: client, source: source, users: EntUsers.Bind(source)}

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

	var request query.Request
	if err := json.Unmarshal([]byte(`{"filter":{"age":{"gte":33}},"sort":["email"],"limit":50}`), &request); err != nil {
		t.Fatal(err)
	}

	got, err := uc.DeactivateUsers(ctx, 1, &request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Matched != 2 || len(got.Deactivated) != 2 {
		t.Fatalf("result = %+v", got)
	}

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

	other, err := client.User.Query().Where(entuser.TenantIDEQ(2)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !other.Active || other.Name != "Other" {
		t.Fatalf("another tenant's row was touched: %+v", other)
	}
}
