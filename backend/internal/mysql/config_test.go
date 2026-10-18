package mysql

import (
	"testing"
	"time"
)

func TestConnectionConfigurationPreservesCredentialsAndVerifiesTLS(t *testing.T) {
	config, err := ConnectionConfig("mysql://owner:p%40ss%3Aword@db.example:3306/blog?tls=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	if config.Passwd != "p@ss:word" || config.TLS == nil || config.TLS.InsecureSkipVerify || config.TLS.ServerName != "db.example" {
		t.Fatal("credentials or verified host configuration lost")
	}
	if !config.ParseTime || config.Loc != time.UTC || config.Params["time_zone"] != "'+00:00'" {
		t.Fatal("UTC date handling lost")
	}
	for _, dsn := range []string{"postgres://owner@db/blog?tls=false", "mysql://owner@db/blog", "mysql://owner@db/blog?tls=skip-verify", "mysql://owner@db/blog?tls=preferred"} {
		if _, err := ConnectionConfig(dsn); err == nil {
			t.Fatal("unsupported or insecure configuration accepted")
		}
	}
}
