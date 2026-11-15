package ondevice

import (
	"fmt"
	"strings"

	"reManager/internal/executor"
	"reManager/internal/vellum"
)

// This app's package repository, which Vellum must keep in its list for the app's updates to arrive.
const (
	RepositoryURL    = "https://nolsen311.github.io/reManager-ondevice"
	repositoriesFile = vellum.VellumRoot + "/etc/apk/repositories"
	// apk finds the key that signed an index by this file name.
	signingKeyPath = vellum.VellumRoot + "/etc/apk/keys/remanager-ondevice.rsa.pub"
)

// SigningKey is the public key that signs this app's packages, the same as packaging/keys/remanager-ondevice.rsa.pub (a test keeps the two equal).
const SigningKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAq22eAr5dYSIxILiioq28
EZ+HusAtoHx8UV5Jz4Tj81K5Bm8tqtI3S2W+QBltK1xDJKgzcOz4pLR0YpU4OtKA
fP2xHODB5MYgX5nYPnGAeBosP6pA/cDiL5jsrbnXl5xjXP/OVUSG4pw8s3L/gaA9
Y0UnE9TPtHMN87iKpM/hUXWtPqUnDnutgTf7soMbORbQoF9gPQUl3Vzf8Z3BE0XJ
XwpSe+Cw8xp+CJZqx0kJ0Y1bjQmU/34MIVidzkKIwnjqF9Yl97QUMGKiCsJs535H
k8sWRscS5CF0uehNHECS6cdccDj/QSd52umqsXOjLFjeOe0l60wNfYHreUpZ9QEO
K1m/2aHaQpnQR9jJgBUIZGrcHytB7YHRootzrc6lnOqIBnxc3tDtNwO8gF/Ur2hl
OQZei3LuLiwVaXzApCsptztqeCojllvOMAeAJ8zsBNQ8tACBTuCjJOZnTuxNou6c
q0GgiyAdn1mrRoqCn5QcjxnzF7XBVXBzjBPU8BMMtYlBi49dqnnlli+AJRDXFAxD
ax1pLu2JhRxywblMBr9NJTP3sTQR4joFAtEOjmVOkYNTbPiWgCqOA4KEzva8Ka3E
erTdp+hOjD/bgenWbHJrMnrZUIOxsOejE1kXlqhifnbOdYvmOsdscPINGUW9kVg0
IH67/w0ooiHVdU66+QveaQ8CAwEAAQ==
-----END PUBLIC KEY-----
`

const FixRestoreRepository = "restore-repository"

// repositoryState reads whether Vellum still lists this app's repository and has its key. ok is false when the files could not be read.
func (s *Service) repositoryState() (listed, keyed, ok bool) {
	out, err := s.exec.ExecuteWithOutput("cat " + repositoriesFile + "; echo @@; cat " + signingKeyPath + " 2>/dev/null")
	parts := strings.SplitN(out, "@@", 2)
	if len(parts) != 2 || (err != nil && strings.TrimSpace(parts[0]) == "") {
		return false, false, false
	}
	for _, line := range strings.Split(parts[0], "\n") {
		if strings.TrimRight(strings.TrimSpace(line), "/") == RepositoryURL {
			listed = true
		}
	}
	keyed = strings.TrimSpace(parts[1]) == strings.TrimSpace(SigningKey)
	return listed, keyed, true
}

// repositoryCheck applies only when this app came from Vellum; a copy put in place by hand gets its updates some other way.
func (s *Service) repositoryCheck(installed map[string]string) (Check, bool) {
	if _, ok := installed[PackageName]; !ok {
		return Check{}, false
	}
	listed, keyed, ok := s.repositoryState()
	if !ok {
		return Check{}, false
	}
	switch {
	case listed && keyed:
		return Check{ID: "repository", OK: true, Title: "Updates for this app are set up"}, true
	case !listed:
		return Check{ID: "repository", Title: "This app's repository is missing from Vellum",
			Detail: "Updates for reManager will not arrive until it is back. Restoring adds " + RepositoryURL + " to Vellum's list.",
			Fix:    FixRestoreRepository}, true
	default:
		return Check{ID: "repository", Title: "This app's signing key is missing from Vellum",
			Detail: "Vellum will refuse updates for reManager until it is back. Restoring puts the key in place again.",
			Fix:    FixRestoreRepository}, true
	}
}

// RestoreRepository puts this app's repository line and signing key back where Vellum looks for them, then refreshes Vellum's index. Both live under /home/root.
func (s *Service) RestoreRepository() error {
	script := "grep -qxF " + RepositoryURL + " " + repositoriesFile + " || echo " + RepositoryURL + " >> " + repositoriesFile + "; " +
		"printf '%s' " + executor.QuoteArg(SigningKey) + " > " + signingKeyPath
	if out, err := s.exec.ExecuteWithOutput(script); err != nil {
		return fmt.Errorf("could not restore the repository: %v %s", err, strings.TrimSpace(out))
	}
	return s.vellum.UpdateIndex()
}
