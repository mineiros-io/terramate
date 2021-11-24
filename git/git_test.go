package git_test

import (
	"errors"
	"os"
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terrastack/git"
	"github.com/mineiros-io/terrastack/test"
)

const CookedCommitID = "a022c39b57b1e711fb9298a05aacc699773e6d36"

func TestGit(t *testing.T) {
	git, err := git.NewWrapper(test.Username, test.Email)
	assert.NoError(t, err, "new git wrapper")

	version, err := git.Version()
	assert.NoError(t, err, "git version")

	t.Logf("git version: %s", version)
}

func TestGitLog(t *testing.T) {
	type testcase struct {
		repo    func(t *testing.T) string
		revs    []string
		want    []git.LogLine
		wantErr error
	}

	for _, tc := range []testcase{
		{
			repo: mkOneCommitRepo,
			revs: []string{"HEAD"},
			want: []git.LogLine{
				{
					CommitID: CookedCommitID,
					Message:  "some message",
				},
			},
			wantErr: nil,
		},
		{
			repo: mkOneCommitRepo,
			revs: []string{"main"},
			want: []git.LogLine{
				{
					CommitID: CookedCommitID,
					Message:  "some message",
				},
			},
			wantErr: nil,
		},
		{
			repo: mkOneCommitRepo,
			revs: []string{"main", "HEAD"},
			want: []git.LogLine{
				{
					CommitID: CookedCommitID,
					Message:  "some message",
				},
			},
			wantErr: nil,
		},
		{
			repo:    mkOneCommitRepo,
			revs:    []string{"^HEAD"},
			want:    []git.LogLine{},
			wantErr: nil,
		},
		{
			repo: mkOneCommitRepo,
			revs: []string{"non-existent-branch"},
			want: []git.LogLine{},

			// we only check if error Is of type CmdError then the state do not
			// matter.
			wantErr: git.NewCmdError("any command", nil, nil),
		},
	} {
		repodir := tc.repo(t)

		gw, err := git.WithConfig(git.Config{
			WorkingDir: repodir,
		})
		assert.NoError(t, err, "new git wrapper")

		logs, err := gw.LogSummary(tc.revs...)

		if tc.wantErr != nil {
			if err == nil {
				t.Errorf("expected error: %v", err)
				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error type mismatch: want[%s] but got [%s]",
					tc.wantErr, err)
			}
		}

		assert.EqualInts(t, len(tc.want), len(logs), "log count mismatch")

		for i := 0; i < len(tc.want); i++ {
			assert.EqualStrings(t, tc.want[i].CommitID, logs[i].CommitID,
				"log commitid mismatch: %s != %s",
				tc.want[i].CommitID, logs[i].CommitID)

			assert.EqualStrings(t, tc.want[i].Message, logs[i].Message,
				"log message mismatch: %s != %s",
				tc.want[i].Message, logs[i].Message)
		}
	}
}

func TestRevParse(t *testing.T) {
	repodir := mkOneCommitRepo(t)

	git := test.NewGitWrapper(t, repodir, false)
	out, err := git.RevParse("main")
	assert.NoError(t, err, "rev-parse failed")
	assert.EqualStrings(t, CookedCommitID, out, "commit mismatch")
}

func TestFetchRemoteRev(t *testing.T) {
	const (
		remote   = "origin"
		revision = "main"
	)

	repodir := mkOneCommitRepo(t)
	git := test.NewGitWrapper(t, repodir, false)

	remoteDir := test.EmptyRepo(t, true)
	err := git.RemoteAdd("origin", remoteDir)
	assert.NoError(t, err)

	err = git.Push("origin", "main")
	assert.NoError(t, err)

	remoteRef, err := git.FetchRemoteRev(remote, revision)
	assert.NoError(t, err, "git.FetchRemoteRev(%q, %q)", remote, revision)

	assert.EqualStrings(
		t,
		CookedCommitID,
		remoteRef.CommitID,
		"remote reference ID doesn't match cooked commit ID",
	)

	const wantRefName = "refs/heads/main"

	assert.EqualStrings(
		t,
		wantRefName,
		remoteRef.Name,
		"remote ref name doesn't match local",
	)

}

func TestFetchRemoteRevErrorHandling(t *testing.T) {
	repodir := mkOneCommitRepo(t)
	git := test.NewGitWrapper(t, repodir, false)
	remoteRef, err := git.FetchRemoteRev("origin", "main")
	assert.Error(t, err, "unexpected result: %v", remoteRef)
}

func mkOneCommitRepo(t *testing.T) string {
	repodir := test.EmptyRepo(t, false)

	// Fixing all the information used to create the SHA-1 below:
	// CommitID: a022c39b57b1e711fb9298a05aacc699773e6d36

	// Other than the environment variables below, the file's permission bits
	// are also used as entropy for the commitid.
	os.Setenv("GIT_COMMITTER_DATE", "1597490918 +0530")
	os.Setenv("GIT_AUTHOR_DATE", "1597490918 +0530")
	os.Setenv("GIT_COMMITTER_NAME", test.Username)
	os.Setenv("GIT_AUTHOR_NAME", test.Username)
	os.Setenv("GIT_COMMITTER_EMAIL", test.Email)
	os.Setenv("GIT_AUTHOR_EMAIL", test.Email)

	defer func() {
		os.Unsetenv("GIT_COMMITTER_DATE")
		os.Unsetenv("GIT_AUTHOR_DATE")
		os.Unsetenv("GIT_COMMITTER_NAME")
		os.Unsetenv("GIT_AUTHOR_NAME")
		os.Unsetenv("GIT_COMMITTER_EMAIL")
		os.Unsetenv("GIT_AUTHOR_EMAIL")
	}()

	gw := test.NewGitWrapper(t, repodir, true)
	filename := test.WriteFile(t, repodir, "README.md", "# Test")
	assert.NoError(t, gw.Add(filename), "git add %s", filename)

	err := gw.Commit("some message")
	assert.NoError(t, err, "commit")

	return repodir
}
