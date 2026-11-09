package store

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Store struct {
	DB *gorm.DB
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	dsn := filepath.Join(dataDir, "mailsender.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	lg := logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
	})
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: lg})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids "database is locked" under concurrency.
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&Account{}, &APIKey{}, &Message{}, &SendAttempt{}, &DailyStat{}, &Suppression{}, &Setting{}); err != nil {
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) GetSetting(key string) (string, bool) {
	var st Setting
	if err := s.DB.First(&st, "key = ?", key).Error; err != nil {
		return "", false
	}
	return st.Value, true
}

func (s *Store) SetSetting(key, value string) error {
	return s.DB.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Setting{Key: key, Value: value}).Error
}

// Day returns the stats bucket key for t in server local time.
func Day(t time.Time) string { return t.Local().Format("2006-01-02") }

// IncStat atomically adds deltas to a daily stats row.
func (s *Store) IncStat(day string, keyID, accID uint, d DailyStat) error {
	d.Day, d.APIKeyID, d.AccountID = day, keyID, accID
	return s.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}, {Name: "api_key_id"}, {Name: "account_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"msg_sent":     gorm.Expr("msg_sent + ?", d.MsgSent),
			"msg_failed":   gorm.Expr("msg_failed + ?", d.MsgFailed),
			"msg_rejected": gorm.Expr("msg_rejected + ?", d.MsgRejected),
			"att_ok":       gorm.Expr("att_ok + ?", d.AttOK),
			"att_fail":     gorm.Expr("att_fail + ?", d.AttFail),
			"att_ms_sum":   gorm.Expr("att_ms_sum + ?", d.AttMsSum),
		}),
	}).Create(&d).Error
}

var ErrNotFound = errors.New("not found")

// Cleanup removes message logs older than the retention window. Daily stats are kept.
func (s *Store) Cleanup(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cut := time.Now().AddDate(0, 0, -retentionDays)
	if err := s.DB.Where("created_at < ?", cut).Delete(&SendAttempt{}).Error; err != nil {
		return 0, err
	}
	res := s.DB.Where("created_at < ? AND status <> ?", cut, StatusQueued).Delete(&Message{})
	return res.RowsAffected, res.Error
}
