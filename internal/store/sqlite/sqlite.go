package sqlite

import (
	dbschema "github.com/elsell/reqdb/db"
	"github.com/elsell/reqdb/internal/store/sqlstore"
	"gorm.io/driver/sqlite"
)

type Store = sqlstore.Store

func Open(path string) (*Store, error) {
	return sqlstore.Open(sqlstore.Config{
		Dialector:                sqlite.Open(path + "?_foreign_keys=on&_busy_timeout=5000"),
		Schema:                   dbschema.Schema,
		SerializeSchemaMigration: true,
	})
}
