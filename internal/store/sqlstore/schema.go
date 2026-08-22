package sqlstore

import (
	"fmt"

	"gorm.io/gorm"
)

func migrate(database *gorm.DB, schema string) error {
	if database.Migrator().HasTable("requirement") {
		if !database.Migrator().HasTable("project") {
			return fmt.Errorf("incompatible database schema; create a new database")
		}
		return nil
	}
	if err := database.Exec(schema).Error; err != nil {
		return fmt.Errorf("initialize database schema: %w", err)
	}
	return nil
}
