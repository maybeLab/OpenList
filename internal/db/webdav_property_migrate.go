package db

import (
	"database/sql"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

// migrateWebDAVProperties prepares existing dead properties for the exact
// hash-based unique index used by WebDAVProperty.
func migrateWebDAVProperties() error {
	table, err := webdavPropertiesTable()
	if err != nil {
		return err
	}
	m := db.Migrator()
	if !m.HasTable(table) {
		return nil
	}

	if !m.HasColumn(table, "key_hash") {
		if err := m.AddColumn(&model.WebDAVProperty{}, "KeyHash"); err != nil {
			return err
		}
	}

	var rows []struct {
		ID        uint
		Path      string
		Namespace string
		Name      string
		KeyHash   sql.NullString
	}
	if err := db.Model(&model.WebDAVProperty{}).Select("id, path, namespace, name, key_hash").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		hash := model.HashWebDAVPropertyKey(row.Path, row.Namespace, row.Name)
		if row.KeyHash.Valid && row.KeyHash.String == hash {
			continue
		}
		if err := db.Model(&model.WebDAVProperty{}).Where("id = ?", row.ID).UpdateColumn("key_hash", hash).Error; err != nil {
			return err
		}
	}

	if m.HasIndex(table, "idx_webdav_property") {
		indexes, err := m.GetIndexes(&model.WebDAVProperty{})
		if err != nil {
			return err
		}
		for _, index := range indexes {
			if index.Name() == "idx_webdav_property" && (len(index.Columns()) != 1 || index.Columns()[0] != "key_hash") {
				if err := dropWebDAVPropertyIndex(table, index.Name()); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func webdavPropertiesTable() (string, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(&model.WebDAVProperty{}); err != nil {
		return "", err
	}
	return stmt.Schema.Table, nil
}

// GORM's PostgreSQL DropIndex emits DROP INDEX CURRENT_SCHEMA().name,
// which PostgreSQL rejects. Resolve and quote the actual index schema instead.
func dropWebDAVPropertyIndex(table, name string) error {
	if db.Dialector.Name() != "postgres" {
		return db.Migrator().DropIndex(table, name)
	}
	var qualified string
	err := db.Raw(`SELECT quote_ident(n.nspname) || '.' || quote_ident(idx.relname)
  FROM pg_index i
  JOIN pg_class idx ON idx.oid = i.indexrelid
  JOIN pg_namespace n ON n.oid = idx.relnamespace
  WHERE i.indrelid = to_regclass(?) AND idx.relname = ?`, quoteIdent(table), name).Scan(&qualified).Error
	if err != nil {
		return err
	}
	if qualified == "" {
		return gorm.ErrRecordNotFound
	}
	return db.Exec("DROP INDEX " + qualified).Error
}
