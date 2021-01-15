package changelog

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shurcooL/githubv4"
	"gopkg.in/src-d/go-billy.v4/memfs"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/storage/memory"
)

type Entry struct {
	Issue string
	Body  string
}

type entryFile struct {
	Contents   []byte
	CommitHash string
}

func Diff(repo, ref1, ref2, entriesDir string) ([]Entry, error) {
	return diffReal(repo, ref1, ref2, entriesDir, "", "", false, nil)
}
func DiffFilenameFmtTimestamp(repoDir, ref1, ref2, entriesDir, repoOwner, repoName string, githubClient *githubv4.Client) ([]Entry, error) {
	return diffReal(repoDir, ref1, ref2, entriesDir, repoOwner, repoName, true, githubClient)
}

func diffReal(repoDir, ref1, ref2, entriesDir, repoOwner, repoName string, timestampFmt bool, githubClient *githubv4.Client) ([]Entry, error) {
	r, err := git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL: repoDir,
	})
	if err != nil {
		return nil, err
	}
	rev2, err := r.ResolveRevision(plumbing.Revision(ref2))
	if err != nil {
		return nil, err
	}
	var rev1 *plumbing.Hash
	if ref1 != "-" {
		rev1, err = r.ResolveRevision(plumbing.Revision(ref1))
		if err != nil {
			return nil, err
		}
	}
	wt, err := r.Worktree()
	if err != nil {
		return nil, err
	}
	err = wt.Checkout(&git.CheckoutOptions{
		Hash:  *rev2,
		Force: true,
	})
	entriesAfterFI, err := wt.Filesystem.ReadDir(entriesDir)
	if err != nil {
		return nil, err
	}
	entriesAfter := make(map[string]entryFile, len(entriesAfterFI))
	for _, i := range entriesAfterFI {
		rootRelFileName := filepath.Join(entriesDir, i.Name())
		f, err := wt.Filesystem.Open(rootRelFileName)
		if err != nil {
			return nil, err
		}
		contents, err := ioutil.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		iter, err := r.Log(&git.LogOptions{
			FileName: &rootRelFileName,
		})
		if err != nil {
			return nil, err
		}
		latestCommit, err := iter.Next()
		if err != nil {
			return nil, fmt.Errorf("found no commits for %q: %s", rootRelFileName, err)
		} else if latestCommit == nil {
			return nil, fmt.Errorf("found no commits for %q", rootRelFileName)
		}

		entriesAfter[i.Name()] = entryFile{
			Contents:   contents,
			CommitHash: latestCommit.Hash.String(),
		}
	}
	if rev1 != nil {
		err = wt.Checkout(&git.CheckoutOptions{
			Hash:  *rev1,
			Force: true,
		})
		entriesBeforeFI, err := wt.Filesystem.ReadDir(entriesDir)
		if err != nil {
			return nil, err
		}
		for _, i := range entriesBeforeFI {
			delete(entriesAfter, i.Name())
		}
	}
	entries := make([]Entry, 0, len(entriesAfter))

	for filename, entry := range entriesAfter {
		var issue string
		if timestampFmt {
			var err error
			issue, err = issueNumForCommit(entry.CommitHash, repoOwner, repoName, githubClient)
			if err != nil {
				return nil, err
			}
		} else {
			issue = strings.TrimSuffix(filename, ".txt")
		}

		entries = append(entries, Entry{
			Issue: issue,
			Body:  string(entry.Contents),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Issue < entries[j].Issue
	})
	return entries, nil
}

func issueNumForCommit(commitHash, repoOwner, repoName string, githubClient *githubv4.Client) (string, error) {
	var q struct {
		Repository struct {
			Commit struct {
				Commit struct {
					AssociatedPullRequests struct {
						Edges []struct {
							Node struct {
								Number githubv4.Int
							}
						}
					} `graphql:"associatedPullRequests(first: 1)"`
				} `graphql:"... on Commit"`
			} `graphql:"object(expression: $sha)"`
		} `graphql:"repository(name: $name, owner: $owner)"`
	}
	variables := map[string]interface{}{
		"owner": githubv4.String(repoOwner),
		"name":  githubv4.String(repoName),
	}
	variables["sha"] = githubv4.String(commitHash)
	err := githubClient.Query(context.Background(), &q, variables)
	if err != nil {
		return "", err
	}
	edges := q.Repository.Commit.Commit.AssociatedPullRequests.Edges
	if len(edges) == 0 {
		return "", fmt.Errorf("could not determine pull request for commit %s", commitHash)
	}
	return fmt.Sprintf("%d", edges[0].Node.Number), nil
}
