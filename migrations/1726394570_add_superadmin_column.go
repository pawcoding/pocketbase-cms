package migrations

import (
	"github.com/pocketbase/dbx"
)

// adds a "lastLoginAlertSentAt" column to all auth collection tables (if not already)
// and makes all existing admins super admins
func init() {
	AppMigrations.Register(func(db dbx.Builder) error {
		_, upErr := db.AddColumn("_admins", "superAdmin", "BOOLEAN DEFAULT 0 NOT NULL").Execute()

		if upErr != nil {
			return upErr
		}

		_, dataErr := db.Update("_admins", dbx.Params{"superAdmin": true}, dbx.NewExp("")).Execute()

		if dataErr != nil {
			return dataErr
		}

		return nil
	}, func(db dbx.Builder) error {
		_, downErr := db.DropColumn("_admins", "superAdmin").Execute()

		if downErr != nil {
			return downErr
		}

		return nil
	})
}
