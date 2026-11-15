//go:build linux

package ondevice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reManager/internal/executor"
)

func TestSigningKeyMatchesTheCommittedKey(t *testing.T) {
	committed, err := os.ReadFile("../../packaging/keys/remanager-ondevice.rsa.pub")
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != SigningKey {
		t.Error("SigningKey differs from packaging/keys/remanager-ondevice.rsa.pub")
	}
}

const vellumRepositories = "/home/root/.vellum/local-repo\nhttps://packages.vellum.delivery\n"

func repositoryTablet(repositories, key string) *fakeTablet {
	return prepend(prepend(healthyTablet(), "list -I", coreInstalled+"tripletap-1.1.1-r0 aarch64 {tripletap} (MIT) [installed]\nremanager-ondevice-0.1.1-r0 aarch64 {remanager-ondevice} (GPL-3.0-only) [installed]\n"),
		"cat /home/root/.vellum/etc/apk/repositories", repositories+"@@\n"+key)
}

func TestRepositoryCheck(t *testing.T) {
	cases := []struct {
		name         string
		repositories string
		key          string
		ok           bool
		title        string
	}{
		{"set up", vellumRepositories + RepositoryURL + "\n", SigningKey, true, "Updates for this app are set up"},
		{"set up with a trailing slash", vellumRepositories + RepositoryURL + "/\n", SigningKey, true, "Updates for this app are set up"},
		{"line missing", vellumRepositories, SigningKey, false, "This app's repository is missing from Vellum"},
		{"key missing", vellumRepositories + RepositoryURL + "\n", "", false, "This app's signing key is missing from Vellum"},
		{"another key under the name", vellumRepositories + RepositoryURL + "\n", "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n", false, "This app's signing key is missing from Vellum"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestService(standardCatalog(), repositoryTablet(c.repositories, c.key))
			got, found := checkByID(s.Checks(false), "repository")
			if !found || got.OK != c.ok || got.Title != c.title {
				t.Errorf("check = %+v (found %v), want ok=%v %q", got, found, c.ok, c.title)
			}
			if !c.ok && got.Fix != FixRestoreRepository {
				t.Errorf("fix = %q", got.Fix)
			}
		})
	}
}

func TestNoRepositoryCheckForACopyInstalledByHand(t *testing.T) {
	s := newTestService(standardCatalog(), prepend(healthyTablet(), "cat /home/root/.vellum/etc/apk/repositories", vellumRepositories+"@@\n"))
	if c, found := checkByID(s.Checks(false), "repository"); found {
		t.Errorf("the app is not a Vellum package here, so its repository does not matter: %+v", c)
	}
}

func TestRestoreRepository(t *testing.T) {
	tablet := healthyTablet()
	s := newTestService(standardCatalog(), tablet)

	if err := s.RestoreRepository(); err != nil {
		t.Fatal(err)
	}
	if !tablet.ran("echo "+RepositoryURL+" >> /home/root/.vellum/etc/apk/repositories") || !tablet.ran("vellum update") {
		t.Errorf("ran %v", tablet.commands)
	}
}

// The restore script, run by a real shell against copies of the two files.
func TestRestoreRepositoryScriptOnDisk(t *testing.T) {
	dir := t.TempDir()
	repositories := filepath.Join(dir, "repositories")
	key := filepath.Join(dir, "remanager-ondevice.rsa.pub")
	os.WriteFile(repositories, []byte(vellumRepositories), 0o644)

	recorder := &fakeTablet{}
	s := newTestService(standardCatalog(), recorder)
	s.RestoreRepository()
	script := strings.NewReplacer(repositoriesFile, repositories, signingKeyPath, key).Replace(recorder.commands[0])

	local := executor.NewLocalExecutor(context.Background())
	for range 2 {
		if out, err := local.ExecuteWithOutput(script); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	gotRepositories, _ := os.ReadFile(repositories)
	if string(gotRepositories) != vellumRepositories+RepositoryURL+"\n" {
		t.Errorf("repositories after two runs = %q", gotRepositories)
	}
	gotKey, _ := os.ReadFile(key)
	if string(gotKey) != SigningKey {
		t.Errorf("key = %q", gotKey)
	}
}
