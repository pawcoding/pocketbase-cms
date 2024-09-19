package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/pawcoding/pocketbase-crm/daos"
	"github.com/pawcoding/pocketbase-crm/models"
	"github.com/pawcoding/pocketbase-crm/tools/archive"
	"github.com/pawcoding/pocketbase-crm/tools/cron"
	"github.com/pawcoding/pocketbase-crm/tools/filesystem"
	"github.com/pawcoding/pocketbase-crm/tools/inflector"
	"github.com/pawcoding/pocketbase-crm/tools/osutils"
	"github.com/pawcoding/pocketbase-crm/tools/security"
)

// Deprecated: Replaced with StoreKeyActiveBackup.
const CacheKeyActiveBackup string = "@activeBackup"

const StoreKeyActiveBackup string = "@activeBackup"

// CreateBackup creates a new backup of the current app pb_data directory.
//
// If name is empty, it will be autogenerated.
// If backup with the same name exists, the new backup file will replace it.
//
// The backup is executed within a transaction, meaning that new writes
// will be temporary "blocked" until the backup file is generated.
//
// To safely perform the backup, it is recommended to have free disk space
// for at least 2x the size of the pb_data directory.
//
// By default backups are stored in pb_data/backups
// (the backups directory itself is excluded from the generated backup).
//
// When using S3 storage for the uploaded collection files, you have to
// take care manually to backup those since they are not part of the pb_data.
//
// Backups can be stored on S3 if it is configured in app.Settings().Backups.
func (app *BaseApp) CreateBackup(ctx context.Context, name string) error {
	if app.Store().Has(StoreKeyActiveBackup) {
		return errors.New("try again later - another backup/restore operation has already been started")
	}

	if name == "" {
		name = app.generateBackupName("pb_backup_")
	}

	app.Store().Set(StoreKeyActiveBackup, name)
	defer app.Store().Remove(StoreKeyActiveBackup)

	// root dir entries to exclude from the backup generation
	exclude := []string{LocalBackupsDirName, LocalTempDirName}

	// make sure that the special temp directory exists
	// note: it needs to be inside the current pb_data to avoid "cross-device link" errors
	localTempDir := filepath.Join(app.DataDir(), LocalTempDirName)
	if err := os.MkdirAll(localTempDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create a temp dir: %w", err)
	}

	// Archive pb_data in a temp directory, exluding the "backups" and the temp dirs.
	//
	// Run in transaction to temporary block other writes (transactions uses the NonconcurrentDB connection).
	// ---
	tempPath := filepath.Join(localTempDir, "pb_backup_"+security.PseudorandomString(4))
	createErr := app.Dao().RunInTransaction(func(dataTXDao *daos.Dao) error {
		return app.LogsDao().RunInTransaction(func(logsTXDao *daos.Dao) error {
			// @todo consider experimenting with temp switching the readonly pragma after the db interface change
			return archive.Create(app.DataDir(), tempPath, exclude...)
		})
	})
	if createErr != nil {
		return createErr
	}
	defer os.Remove(tempPath)

	// Persist the backup in the backups filesystem.
	// ---
	fsys, err := app.NewBackupsFilesystem()
	if err != nil {
		return err
	}
	defer fsys.Close()

	fsys.SetContext(ctx)

	file, err := filesystem.NewFileFromPath(tempPath)
	if err != nil {
		return err
	}
	file.OriginalName = name
	file.Name = file.OriginalName

	if err := fsys.UploadFile(file, file.Name); err != nil {
		return err
	}

	return nil
}

// RestoreBackup restores the backup with the specified name and restarts
// the current running application process.
//
// NB! This feature is experimental and currently is expected to work only on UNIX based systems.
//
// To safely perform the restore it is recommended to have free disk space
// for at least 2x the size of the restored pb_data backup.
//
// The performed steps are:
//
//  1. Download the backup with the specified name in a temp location
//     (this is in case of S3; otherwise it creates a temp copy of the zip)
//
//  2. Extract the backup in a temp directory inside the app "pb_data"
//     (eg. "pb_data/.pb_temp_to_delete/pb_restore").
//
//  3. Move the current app "pb_data" content (excluding the local backups and the special temp dir)
//     under another temp sub dir that will be deleted on the next app start up
//     (eg. "pb_data/.pb_temp_to_delete/old_pb_data").
//     This is because on some environments it may not be allowed
//     to delete the currently open "pb_data" files.
//
//  4. Move the extracted dir content to the app "pb_data".
//
//  5. Restart the app (on successful app bootstap it will also remove the old pb_data).
//
// If a failure occure during the restore process the dir changes are reverted.
// If for whatever reason the revert is not possible, it panics.
func (app *BaseApp) RestoreBackup(ctx context.Context, name string) error {
	if runtime.GOOS == "windows" {
		return errors.New("restore is not supported on windows")
	}

	if app.Store().Has(StoreKeyActiveBackup) {
		return errors.New("try again later - another backup/restore operation has already been started")
	}

	app.Store().Set(StoreKeyActiveBackup, name)
	defer app.Store().Remove(StoreKeyActiveBackup)

	fsys, err := app.NewBackupsFilesystem()
	if err != nil {
		return err
	}
	defer fsys.Close()

	fsys.SetContext(ctx)

	// fetch the backup file in a temp location
	br, err := fsys.GetFile(name)
	if err != nil {
		return err
	}
	defer br.Close()

	// make sure that the special temp directory exists
	// note: it needs to be inside the current pb_data to avoid "cross-device link" errors
	localTempDir := filepath.Join(app.DataDir(), LocalTempDirName)
	if err := os.MkdirAll(localTempDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create a temp dir: %w", err)
	}

	// create a temp zip file from the blob.Reader and try to extract it
	tempZip, err := os.CreateTemp(localTempDir, "pb_restore_zip")
	if err != nil {
		return err
	}
	defer os.Remove(tempZip.Name())

	if _, err := io.Copy(tempZip, br); err != nil {
		return err
	}

	extractedDataDir := filepath.Join(localTempDir, "pb_restore_"+security.PseudorandomString(4))
	defer os.RemoveAll(extractedDataDir)
	if err := archive.Extract(tempZip.Name(), extractedDataDir); err != nil {
		return err
	}

	// ensure that a database file exists
	extractedDB := filepath.Join(extractedDataDir, "data.db")
	if _, err := os.Stat(extractedDB); err != nil {
		return fmt.Errorf("data.db file is missing or invalid: %w", err)
	}

	// remove the extracted zip file since we no longer need it
	// (this is in case the app restarts and the defer calls are not called)
	if err := os.Remove(tempZip.Name()); err != nil {
		app.Logger().Debug(
			"[RestoreBackup] Failed to remove the temp zip backup file",
			slog.String("file", tempZip.Name()),
			slog.String("error", err.Error()),
		)
	}

	// root dir entries to exclude from the backup restore
	exclude := []string{LocalBackupsDirName, LocalTempDirName}

	// move the current pb_data content to a special temp location
	// that will hold the old data between dirs replace
	// (the temp dir will be automatically removed on the next app start)
	oldTempDataDir := filepath.Join(localTempDir, "old_pb_data_"+security.PseudorandomString(4))
	if err := osutils.MoveDirContent(app.DataDir(), oldTempDataDir, exclude...); err != nil {
		return fmt.Errorf("failed to move the current pb_data content to a temp location: %w", err)
	}

	// move the extracted archive content to the app's pb_data
	if err := osutils.MoveDirContent(extractedDataDir, app.DataDir(), exclude...); err != nil {
		return fmt.Errorf("failed to move the extracted archive content to pb_data: %w", err)
	}

	revertDataDirChanges := func() error {
		if err := osutils.MoveDirContent(app.DataDir(), extractedDataDir, exclude...); err != nil {
			return fmt.Errorf("failed to revert the extracted dir change: %w", err)
		}

		if err := osutils.MoveDirContent(oldTempDataDir, app.DataDir(), exclude...); err != nil {
			return fmt.Errorf("failed to revert old pb_data dir change: %w", err)
		}

		return nil
	}

	// restart the app
	if err := app.Restart(); err != nil {
		if revertErr := revertDataDirChanges(); revertErr != nil {
			panic(revertErr)
		}

		return fmt.Errorf("failed to restart the app process: %w", err)
	}

	return nil
}

// initAutobackupHooks registers the autobackup app serve hooks.
func (app *BaseApp) initAutobackupHooks() error {
	c := cron.New()
	isServe := false

	loadJob := func() {
		c.Stop()

		// make sure that app.Settings() is always up to date
		//
		// @todo remove with the refactoring as core.App and daos.Dao will be one.
		if err := app.RefreshSettings(); err != nil {
			app.Logger().Debug(
				"[Backup cron] Failed to get the latest app settings",
				slog.String("error", err.Error()),
			)
		}

		rawSchedule := app.Settings().Backups.Cron
		if rawSchedule == "" || !isServe || !app.IsBootstrapped() {
			return
		}

		c.Add("@autobackup", rawSchedule, func() {
			const autoPrefix = "@auto_pb_backup_"

			name := app.generateBackupName(autoPrefix)

			if err := app.CreateBackup(context.Background(), name); err != nil {
				app.Logger().Debug(
					"[Backup cron] Failed to create backup",
					slog.String("name", name),
					slog.String("error", err.Error()),
				)
			}

			maxKeep := app.Settings().Backups.CronMaxKeep

			if maxKeep == 0 {
				return // no explicit limit
			}

			fsys, err := app.NewBackupsFilesystem()
			if err != nil {
				app.Logger().Debug(
					"[Backup cron] Failed to initialize the backup filesystem",
					slog.String("error", err.Error()),
				)
				return
			}
			defer fsys.Close()

			files, err := fsys.List(autoPrefix)
			if err != nil {
				app.Logger().Debug(
					"[Backup cron] Failed to list autogenerated backups",
					slog.String("error", err.Error()),
				)
				return
			}

			if maxKeep >= len(files) {
				return // nothing to remove
			}

			// sort desc
			sort.Slice(files, func(i, j int) bool {
				return files[i].ModTime.After(files[j].ModTime)
			})

			// keep only the most recent n auto backup files
			toRemove := files[maxKeep:]

			for _, f := range toRemove {
				if err := fsys.Delete(f.Key); err != nil {
					app.Logger().Debug(
						"[Backup cron] Failed to remove old autogenerated backup",
						slog.String("key", f.Key),
						slog.String("error", err.Error()),
					)
				}
			}
		})

		// restart the ticker
		c.Start()
	}

	// load on app serve
	app.OnBeforeServe().Add(func(e *ServeEvent) error {
		isServe = true
		loadJob()
		return nil
	})

	// stop the ticker on app termination
	app.OnTerminate().Add(func(e *TerminateEvent) error {
		c.Stop()
		return nil
	})

	// reload on app settings change
	app.OnModelAfterUpdate((&models.Param{}).TableName()).Add(func(e *ModelEvent) error {
		p := e.Model.(*models.Param)
		if p == nil || p.Key != models.ParamAppSettings {
			return nil
		}

		loadJob()

		return nil
	})

	return nil
}

func (app *BaseApp) generateBackupName(prefix string) string {
	appName := inflector.Snakecase(app.Settings().Meta.AppName)
	if len(appName) > 50 {
		appName = appName[:50]
	}

	return fmt.Sprintf(
		"%s%s_%s.zip",
		prefix,
		appName,
		time.Now().UTC().Format("20060102150405"),
	)
}
