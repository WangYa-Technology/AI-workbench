package assets_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"strings"
	"testing"
)

func TestAssetUnicodeTextContract(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	owner := uuid.New()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,'unicode_owner','Unicode','creator')`, owner, owner.String()+"@test.local"); err != nil {
		t.Fatal(err)
	}
	s := assets.NewService(pool, t.TempDir())
	for _, text := range []string{strings.Repeat("a", 120), strings.Repeat("图", 120), strings.Repeat("😀", 120), strings.Repeat("e\u0301", 60)} {
		a, err := s.Upload(ctx, owner, assets.UploadInput{Title: text, Filename: strings.Repeat("图", 176) + ".txt", Reader: strings.NewReader("unicode contract")})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.UploadVersion(ctx, owner, a.ID, assets.VersionInput{UploadInput: assets.UploadInput{Title: text, Filename: "version.txt", Reader: strings.NewReader("version contract")}, Note: strings.Repeat("😀", 500)})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"ab", strings.Repeat("图", 121), strings.Repeat("😀", 121), strings.Repeat("e\u0301", 61), "abc\xff"} {
		_, err := s.Upload(ctx, owner, assets.UploadInput{Title: text, Filename: "a.txt", Reader: strings.NewReader("bad title")})
		if !errors.Is(err, assets.ErrInvalid) {
			t.Fatalf("invalid title accepted: %v", err)
		}
	}
	base, err := s.Upload(ctx, owner, assets.UploadInput{Title: "Valid", Filename: "base.txt", Reader: strings.NewReader("base")})
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range []string{"ab", strings.Repeat("图", 501), "abc\xff"} {
		_, err = s.UploadVersion(ctx, owner, base.ID, assets.VersionInput{UploadInput: assets.UploadInput{Title: "Valid", Filename: "v.txt", Reader: strings.NewReader("version")}, Note: note})
		if !errors.Is(err, assets.ErrInvalid) {
			t.Fatal("invalid note", err)
		}
	}
	for _, name := range []string{strings.Repeat("图", 177) + ".txt", "a\xff.txt"} {
		_, err = s.Upload(ctx, owner, assets.UploadInput{Title: "Valid", Filename: name, Reader: strings.NewReader("bad filename")})
		if !errors.Is(err, assets.ErrInvalid) {
			t.Fatal("invalid filename", err)
		}
	}
}
