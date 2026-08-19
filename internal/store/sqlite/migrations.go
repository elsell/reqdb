package sqlite

import (
	"fmt"

	dbschema "github.com/elsell/reqdb/db"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

const requestIDsMigrationID = "202608180001"
const requirementDependencyMigrationID = "202608180002"
const requirementLifecycleMigrationID = "202608180003"
const workflowStateMigrationID = "202608190001"

type migrationRecord struct {
	ID string `gorm:"column:id;primaryKey;size:255"`
}

func (migrationRecord) TableName() string { return "schema_migrations" }

func migrate(database *gorm.DB) error {
	if err := baselineLegacyDatabase(database); err != nil {
		return err
	}
	if err := database.Exec(`PRAGMA foreign_keys = OFF`).Error; err != nil {
		return fmt.Errorf("disable foreign keys for database migrations: %w", err)
	}
	restoreForeignKeys := func() error {
		if err := database.Exec(`PRAGMA foreign_keys = ON`).Error; err != nil {
			return fmt.Errorf("enable foreign keys after database migrations: %w", err)
		}
		return nil
	}
	options := &gormigrate.Options{
		TableName:                 "schema_migrations",
		IDColumnName:              "id",
		IDColumnSize:              255,
		UseTransaction:            true,
		ValidateUnknownMigrations: true,
	}
	migrator := gormigrate.New(database, options, migrations())
	migrator.InitSchema(func(tx *gorm.DB) error {
		return tx.Exec(dbschema.Schema).Error
	})
	if err := migrator.Migrate(); err != nil {
		_ = restoreForeignKeys()
		return fmt.Errorf("apply database migrations: %w", err)
	}
	if err := restoreForeignKeys(); err != nil {
		return err
	}
	type foreignKeyViolation struct {
		Table        string
		RowID        int64 `gorm:"column:rowid"`
		Parent       string
		ForeignKeyID int `gorm:"column:fkid"`
	}
	var violations []foreignKeyViolation
	if err := database.Raw(`PRAGMA foreign_key_check`).Scan(&violations).Error; err != nil {
		return fmt.Errorf("check foreign keys after database migrations: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf("database migration left %d foreign key violation(s)", len(violations))
	}
	return nil
}

func migrations() []*gormigrate.Migration {
	return []*gormigrate.Migration{
		{
			ID: requestIDsMigrationID,
			Migrate: func(tx *gorm.DB) error {
				if err := tx.Exec(`ALTER TABLE audit_event ADD COLUMN correlation_id TEXT NOT NULL DEFAULT ''`).Error; err != nil {
					return err
				}
				if err := tx.Exec(`ALTER TABLE audit_event ADD COLUMN causation_id TEXT NOT NULL DEFAULT ''`).Error; err != nil {
					return err
				}
				return tx.Exec(`DROP TRIGGER IF EXISTS audit_event_no_delete`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				if err := tx.Migrator().DropColumn("audit_event", "causation_id"); err != nil {
					return err
				}
				if err := tx.Migrator().DropColumn("audit_event", "correlation_id"); err != nil {
					return err
				}
				return tx.Exec(`
CREATE TRIGGER audit_event_no_delete
BEFORE DELETE ON audit_event
BEGIN
    SELECT RAISE(ABORT, 'audit event is append-only');
END`).Error
			},
		},
		{
			ID: requirementDependencyMigrationID,
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(dbschema.RequirementDependencyMigration).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable("requirement_dependency")
			},
		},
		{
			ID: requirementLifecycleMigrationID,
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(dbschema.RequirementLifecycleMigration).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropColumn("requirement", "lifecycle_state")
			},
		},
		{
			ID: workflowStateMigrationID,
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(dbschema.WorkflowStateMigration).Error
			},
		},
	}
}

func baselineLegacyDatabase(database *gorm.DB) error {
	if !database.Migrator().HasTable("requirement") || database.Migrator().HasTable("schema_migrations") {
		return nil
	}
	if err := database.AutoMigrate(&migrationRecord{}); err != nil {
		return fmt.Errorf("create migration baseline: %w", err)
	}
	if err := database.Create(&migrationRecord{ID: "SCHEMA_INIT"}).Error; err != nil {
		return fmt.Errorf("record migration baseline: %w", err)
	}
	hasCorrelation := database.Migrator().HasColumn("audit_event", "correlation_id")
	hasCausation := database.Migrator().HasColumn("audit_event", "causation_id")
	if hasCorrelation != hasCausation {
		return fmt.Errorf("legacy audit schema has only one request ID column")
	}
	if hasCorrelation {
		if err := database.Exec(`DROP TRIGGER IF EXISTS audit_event_no_delete`).Error; err != nil {
			return fmt.Errorf("finish request ID migration baseline: %w", err)
		}
		if err := database.Create(&migrationRecord{ID: requestIDsMigrationID}).Error; err != nil {
			return fmt.Errorf("record request ID migration baseline: %w", err)
		}
	}
	if database.Migrator().HasColumn("requirement", "lifecycle_state") {
		if err := database.Create(&migrationRecord{ID: requirementLifecycleMigrationID}).Error; err != nil {
			return fmt.Errorf("record requirement lifecycle migration baseline: %w", err)
		}
	}
	if database.Migrator().HasTable("state_history") {
		if err := database.Create(&migrationRecord{ID: workflowStateMigrationID}).Error; err != nil {
			return fmt.Errorf("record workflow state migration baseline: %w", err)
		}
	}
	return nil
}
