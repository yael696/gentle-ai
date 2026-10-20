package reviewtransaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	RepositoryReviewPolicyPath   = ".gentle-ai/review-policy.json"
	RepositoryReviewPolicySchema = "gentle-ai.repository-review-policy/v1"
	maxRepositoryReviewPolicy    = 16 << 10
)

type repositoryReviewPolicy struct {
	Schema                  string `json:"schema"`
	MandatoryIsolatedReview *bool  `json:"mandatory_isolated_review"`
}

// RepositoryMandatoryIsolatedReviewEnabled unions the local policy sources.
// No local source proves that policy was never removed from committed history.
func (builder SnapshotBuilder) RepositoryMandatoryIsolatedReviewEnabled(ctx context.Context) (bool, error) {
	root, err := builder.repositoryRoot(ctx)
	if err != nil {
		return false, err
	}
	enabled, err := repositoryReviewPolicyWorkspaceFile(root)
	if err != nil || enabled {
		return enabled, err
	}
	head, err := runGit(ctx, root, nil, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		var gitErr *GitCommandError
		if !errors.As(err, &gitErr) || gitErr.ExitCode != 1 {
			return false, err
		}
	} else {
		enabled, err = repositoryReviewPolicyTreeValue(ctx, root, strings.TrimSpace(string(head)))
		if err != nil || enabled {
			return enabled, err
		}
	}
	// Discover the default index first; ambient GIT_INDEX_FILE cannot hide it.
	listing, probeErr := runGit(ctx, root, nil, nil, "ls-files", "--stage", "-z", "--", ":(literal)"+RepositoryReviewPolicyPath)
	if probeErr == nil {
		enabled, err = repositoryReviewPolicyIndexValue(ctx, root, listing)
		if err != nil || enabled {
			return enabled, err
		}
	}
	alternate, err := builder.alternateCommitIndex(ctx)
	if err != nil {
		return false, err
	}
	if alternate != "" {
		listing, err := runGit(ctx, root, []string{"GIT_INDEX_FILE=" + alternate}, nil, "ls-files", "--stage", "-z", "--", ":(literal)"+RepositoryReviewPolicyPath)
		if err != nil {
			return false, fmt.Errorf("cannot verify selected Git index review policy: %w", err)
		}
		enabled, err = repositoryReviewPolicyIndexValue(ctx, root, listing)
		if err != nil || enabled {
			return enabled, err
		}
		if probeErr != nil {
			return false, fmt.Errorf("cannot verify default Git index review policy with an alternate index selected: %w", probeErr)
		}
	}
	// Preserve upstream informational handling of an unreadable default index
	// when no alternate is selected. This is not proof that policy is absent.
	// An established mandatory policy instead requires the valid staged snapshot.
	return false, nil
}

func repositoryReviewPolicyIndexValue(ctx context.Context, root string, listing []byte) (bool, error) {
	if len(listing) == 0 {
		return false, nil
	}
	entries := bytes.Split(listing, []byte{0})
	if len(entries) != 2 {
		return false, errors.New("repository review policy has unresolved index entries")
	}
	header, path, found := bytes.Cut(entries[0], []byte{'\t'})
	fields := strings.Fields(string(header))
	if !found || string(path) != RepositoryReviewPolicyPath || len(fields) != 3 || fields[2] != "0" {
		return false, errors.New("repository review policy index entry is malformed or unmerged")
	}
	// Reuse the same regular-file, blob-size and JSON checks as frozen trees.
	content, _, err := repositoryReviewPolicyListing(ctx, root, []byte(fields[0]+" blob "+fields[1]+"\t"+string(path)+"\x00"))
	if err != nil {
		return false, err
	}
	return parseRepositoryReviewPolicy(content)
}

// ValidateDefaultCommitIndex refuses overrides the staged projection cannot represent.
func (builder SnapshotBuilder) ValidateDefaultCommitIndex(ctx context.Context) error {
	index, err := builder.alternateCommitIndex(ctx)
	if err != nil {
		return err
	}
	if index != "" {
		return errors.New("mandatory pre-commit validation cannot validate an alternate GIT_INDEX_FILE; review and commit the default staged index")
	}
	return nil
}

func (builder SnapshotBuilder) alternateCommitIndex(ctx context.Context) (string, error) {
	index := os.Getenv("GIT_INDEX_FILE")
	if index == "" {
		return "", nil
	}
	root, err := builder.repositoryRoot(ctx)
	if err != nil {
		return "", err
	}
	output, err := runGit(ctx, root, nil, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	absolute := func(path string) string {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return filepath.Clean(path)
	}
	if absolute(index) != absolute(strings.TrimSpace(string(output))) {
		return index, nil
	}
	return "", nil
}

// SnapshotRequiresMandatoryIsolatedReview binds START and capture behavior to
// the same immutable tree policy the authority froze.
func (builder SnapshotBuilder) SnapshotRequiresMandatoryIsolatedReview(ctx context.Context, snapshot Snapshot) (bool, error) {
	root, err := builder.repositoryRoot(ctx)
	if err != nil {
		return false, err
	}
	baseEnabled, err := repositoryReviewPolicyTreeValue(ctx, root, snapshot.BaseTree)
	if err != nil {
		return false, err
	}
	candidateEnabled, err := repositoryReviewPolicyTreeValue(ctx, root, snapshot.CandidateTree)
	if err != nil {
		return false, err
	}
	return baseEnabled || candidateEnabled, nil
}

func repositoryReviewPolicyTreeValue(ctx context.Context, root string, tree string) (bool, error) {
	content, present, err := repositoryReviewPolicyTreeFile(ctx, root, tree)
	if err != nil || !present {
		return false, err
	}
	enabled, err := parseRepositoryReviewPolicy(content)
	if err != nil {
		return false, fmt.Errorf("parse frozen %s: %w", RepositoryReviewPolicyPath, err)
	}
	return enabled, nil
}

func repositoryReviewPolicyTreeFile(ctx context.Context, root string, tree string) ([]byte, bool, error) {
	listing, err := runGit(ctx, root, nil, nil,
		"ls-tree", "-z", "--full-tree", tree, "--", ":(literal)"+RepositoryReviewPolicyPath)
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s in Git tree: %w", RepositoryReviewPolicyPath, err)
	}
	if len(listing) == 0 {
		return nil, false, nil
	}
	return repositoryReviewPolicyListing(ctx, root, listing)
}

func repositoryReviewPolicyListing(ctx context.Context, root string, listing []byte) ([]byte, bool, error) {
	entries := bytes.Split(listing, []byte{0})
	if len(entries) != 2 || len(entries[0]) == 0 {
		return nil, false, errors.New("repository review policy tree listing is malformed")
	}
	header, path, found := bytes.Cut(entries[0], []byte{'\t'})
	if !found || string(path) != RepositoryReviewPolicyPath {
		return nil, false, errors.New("repository review policy tree listing names an unexpected path")
	}
	fields := strings.Fields(string(header))
	if len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" || !exactObjectPattern.MatchString(fields[2]) {
		return nil, false, errors.New("repository review policy must be a regular tracked file")
	}
	content, err := runGitLimited(ctx, root, nil, nil, maxRepositoryReviewPolicy,
		"cat-file", "blob", fields[2])
	if err != nil {
		return nil, false, fmt.Errorf("read %s from Git tree: %w", RepositoryReviewPolicyPath, err)
	}
	return content, true, nil
}

func repositoryReviewPolicyWorkspaceFile(root string) (bool, error) {
	directory := filepath.Join(root, ".gentle-ai")
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() {
			// A path that cannot hold a policy has no mandatory-policy meaning.
			if info.Mode()&os.ModeSymlink == 0 {
				return false, nil
			}
			if _, policyErr := os.Stat(filepath.Join(directory, "review-policy.json")); errors.Is(policyErr, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("%s must be a real directory", filepath.ToSlash(filepath.Join(".gentle-ai")))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect workspace review policy directory: %w", err)
	} else {
		return false, nil
	}
	path := filepath.Join(root, filepath.FromSlash(RepositoryReviewPolicyPath))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect workspace %s: %w", RepositoryReviewPolicyPath, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxRepositoryReviewPolicy {
		return false, fmt.Errorf("workspace %s must be a regular file no larger than %d bytes", RepositoryReviewPolicyPath, maxRepositoryReviewPolicy)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read workspace %s: %w", RepositoryReviewPolicyPath, err)
	}
	return parseRepositoryReviewPolicy(content)
}

func parseRepositoryReviewPolicy(content []byte) (bool, error) {
	if len(content) == 0 || len(content) > maxRepositoryReviewPolicy {
		return false, errors.New("policy content is empty or exceeds its size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false, errors.New("policy must contain a JSON object")
	}
	var policy repositoryReviewPolicy
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return false, errors.New("policy contains a duplicate or invalid field")
		}
		seen[key] = true
		switch key {
		case "schema":
			err = decoder.Decode(&policy.Schema)
		case "mandatory_isolated_review":
			err = decoder.Decode(&policy.MandatoryIsolatedReview)
		default:
			return false, fmt.Errorf("unknown policy field %q", key)
		}
		if err != nil {
			return false, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return false, errors.New("policy must contain exactly one JSON object")
		}
		return false, err
	}
	if policy.Schema != RepositoryReviewPolicySchema || policy.MandatoryIsolatedReview == nil {
		return false, errors.New("policy requires the supported schema and mandatory_isolated_review boolean")
	}
	return *policy.MandatoryIsolatedReview, nil
}
