package sqlite

import (
	"fmt"

	dbschema "github.com/elsell/reqdb/db"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

const requestIDsMigrationID = "202608180001"

type migrationRecord struct {
	ID string `gorm:"column:id;primaryKey;size:255"`
}

func (migrationRecord) TableName() string { return "schema_migrations" }

func migrate(database *gorm.DB) error {
	if err := baselineLegacyDatabase(database); err != nil {
		return err
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
		return fmt.Errorf("apply database migrations: %w", err)
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
	return nil
}
