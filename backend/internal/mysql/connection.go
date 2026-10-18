package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	driver "github.com/go-sql-driver/mysql"
	"net/url"
	"os"
	"strings"
	"time"
)

func Open(ctx context.Context, dsn string) (*Store, error) {
	config, err := ConnectionConfig(dsn)
	if err != nil {
		return nil, err
	}
	connector, err := driver.NewConnector(config)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// Application URLs stay readable in secret configuration. The driver receives a
// structured config, so passwords are never concatenated into driver syntax.
func ConnectionConfig(dsn string) (*driver.Config, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "mysql" || u.User == nil || u.Hostname() == "" || strings.TrimPrefix(u.Path, "/") == "" {
		return nil, errors.New("invalid MySQL URL")
	}
	config := driver.NewConfig()
	config.User = u.User.Username()
	config.Passwd, _ = u.User.Password()
	config.Net = "tcp"
	config.Addr = u.Host
	config.DBName = strings.TrimPrefix(u.Path, "/")
	config.ParseTime = true
	config.Loc = time.UTC
	config.Timeout = 10 * time.Second
	config.ReadTimeout = 30 * time.Second
	config.WriteTimeout = 30 * time.Second
	config.Collation = "utf8mb4_0900_bin"
	config.Params = map[string]string{"time_zone": "'+00:00'"}
	config.ClientFoundRows = true
	if u.Query().Get("tls") == "verify-full" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		path := u.Query().Get("sslrootcert")
		if path == "" {
			path = os.Getenv("DATABASE_CA_FILE")
		}
		if path != "" {
			pem, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if !roots.AppendCertsFromPEM(pem) {
				return nil, errors.New("invalid database CA")
			}
		}
		config.TLS = &tls.Config{RootCAs: roots, ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	} else if u.Query().Get("tls") != "false" {
		return nil, errors.New("MySQL TLS mode must be explicit")
	}
	return config, nil
}
