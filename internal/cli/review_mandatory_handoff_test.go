package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewtransaction"
)

func TestMandatoryHandoffNoPolicyDamagedIndexMatchesUpstream(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	args := []string{"validate", "--cwd", repo, "--gate", "pre-commit"}
	var before, after bytes.Buffer
	if err := RunReview(args, &before); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "index"), []byte("damaged-index\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunReview(args, &after); err != nil {
		t.Fatalf("no-policy validation read damaged index: %v", err)
	}
	if before.String() != after.String() {
		t.Fatalf("informational result changed: %s / %s", &before, &after)
	}
}

func TestMandatoryHandoffRejectsPrePolicyUnrestrictedApproval(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n", 0o644)
	started := startFacadeReviewForTarget(t, repo, model.AgentCodex, reviewtransaction.Target{Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionStaged, IntendedUntracked: []string{}})
	// Deliberately use unrestricted manual capture before policy activation.
	for order := range started.SelectedLenses {
		captureCleanCLIReviewerResult(t, repo, started, order, &bytes.Buffer{})
	}
	path := filepath.Join(repo, ".gentle-ai", "review-policy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(mandatoryReviewPolicyFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := mandatoryReviewValidate(t, repo, started.LineageID); err == nil || result.Allowed {
		t.Fatalf("pre-policy unrestricted approval accepted: %#v %v", result, err)
	}
}

func TestMandatoryHandoffPublicationIsNeverVerifiedByCallerBase(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/unreviewed.go", "package unreviewed\n", 0o644)
	runReviewCLIGit(t, repo, "commit", "-qm", "unreviewed code")
	writeReviewStartCandidate(t, repo, "docs/guide.md", "Passive camouflage.\n", 0o644)
	runReviewCLIGit(t, repo, "commit", "-qm", "passive tail")
	for _, gate := range []string{"pre-push", "pre-pr", "release"} {
		for _, base := range []string{"", "HEAD", "missing", "HEAD^{tree}", "HEAD~1"} {
			for _, legacy := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/legacy=%v", gate, base, legacy), func(t *testing.T) {
					var out bytes.Buffer
					args := []string{"--cwd", repo, "--gate", gate, "--base-ref", base}
					var err error
					if legacy {
						err = RunReviewValidateNonDeciding(args, &out)
					} else {
						err = RunReview(append([]string{"validate"}, args...), &out)
					}
					var result ReviewValidateResult
					if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
						t.Fatal(decodeErr)
					}
					if err == nil || result.Allowed || !strings.Contains(result.Reason, "unverified") {
						t.Fatalf("gate=%s base=%s result=%#v err=%v", gate, base, result, err)
					}
				})
			}
		}
	}
	// Historical policy removal is not locally discoverable without a trusted base.
	runReviewCLIGit(t, repo, "rm", ".gentle-ai/review-policy.json")
	runReviewCLIGit(t, repo, "commit", "-qm", "remove policy")
	for _, base := range []string{"HEAD", "HEAD~1"} {
		var out bytes.Buffer
		if err := RunReview([]string{"validate", "--cwd", repo, "--gate", "pre-push", "--base-ref", base}, &out); err != nil {
			t.Fatal(err)
		}
		var result ReviewValidateResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Allowed || result.Delivery != reviewtransaction.RDDDeliveryUnmanaged {
			t.Fatalf("removed policy was treated as publication proof: %s", &out)
		}
	}
}
