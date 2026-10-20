package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewtransaction"
)

func TestMandatoryPolicyDiscoveryLocalSources(t *testing.T) {
	disabled := strings.Replace(mandatoryReviewPolicyFixture, ":true", ":false", 1)
	for _, scenario := range []string{
		"HEAD enabled workspace deleted",
		"HEAD enabled index deleted",
		"HEAD enabled workspace disabled",
		"index only enabled",
		"unborn index only enabled",
		"index enabled workspace disabled",
		"HEAD disabled index enabled",
		"default index enabled alternate index empty",
	} {
		t.Run(scenario, func(t *testing.T) {
			reviewEnabledHome(t)
			repo := initReviewCLIRepo(t)
			policy := filepath.Join(repo, ".gentle-ai", "review-policy.json")
			if strings.HasPrefix(scenario, "HEAD enabled") {
				enableMandatoryReviewPolicy(t, repo)
			} else {
				writeReviewStartCandidate(t, repo, reviewtransaction.RepositoryReviewPolicyPath, mandatoryReviewPolicyFixture, 0o644)
			}
			if scenario == "unborn index only enabled" {
				runReviewCLIGit(t, repo, "update-ref", "-d", "HEAD")
			}
			if scenario == "HEAD disabled index enabled" {
				if err := os.WriteFile(policy, []byte(disabled), 0o644); err != nil {
					t.Fatal(err)
				}
				runReviewCLIGit(t, repo, "add", reviewtransaction.RepositoryReviewPolicyPath)
				runReviewCLIGit(t, repo, "commit", "-qm", "disabled policy")
				if err := os.WriteFile(policy, []byte(mandatoryReviewPolicyFixture), 0o644); err != nil {
					t.Fatal(err)
				}
				runReviewCLIGit(t, repo, "add", reviewtransaction.RepositoryReviewPolicyPath)
			}
			switch scenario {
			case "HEAD enabled index deleted":
				runReviewCLIGit(t, repo, "rm", reviewtransaction.RepositoryReviewPolicyPath)
			case "HEAD enabled workspace disabled", "index enabled workspace disabled":
				if err := os.WriteFile(policy, []byte(disabled), 0o644); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.RemoveAll(filepath.Dir(policy)); err != nil {
					t.Fatal(err)
				}
			}
			writeReviewStartCandidate(t, repo, "internal/unreviewed.go", "package unreviewed\n", 0o644)
			if scenario == "default index enabled alternate index empty" {
				t.Setenv("GIT_INDEX_FILE", filepath.Join(repo, ".git", "missing-alternate-index"))
			}
			enabled, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).RepositoryMandatoryIsolatedReviewEnabled(t.Context())
			if err != nil || !enabled {
				t.Errorf("local policy was hidden: enabled=%v err=%v", enabled, err)
			}
			result, err := mandatoryReviewValidate(t, repo, "")
			if err == nil || result.Allowed {
				t.Fatalf("unreviewed staged code escaped mandatory enforcement: %#v %v", result, err)
			}
		})
	}
}

func TestMandatoryPolicyDiscoveryRejectsInvalidReplacement(t *testing.T) {
	for _, source := range []string{"workspace", "index", "index-only"} {
		for _, invalid := range []string{
			`{`,
			`null`,
			`{"schema":"unsupported","mandatory_isolated_review":false}`,
			`{"schema":"gentle-ai.repository-review-policy/v1","mandatory_isolated_review":"false"}`,
			`{"schema":"gentle-ai.repository-review-policy/v1","mandatory_isolated_review":true,"mandatory_isolated_review":false}`,
			`{"schema":"gentle-ai.repository-review-policy/v1","mandatory_isolated_review":false,"unknown":true}`,
		} {
			t.Run(source+"/"+invalid, func(t *testing.T) {
				reviewEnabledHome(t)
				repo := initReviewCLIRepo(t)
				if source != "index-only" {
					enableMandatoryReviewPolicy(t, repo)
				}
				policy := filepath.Join(repo, filepath.FromSlash(reviewtransaction.RepositoryReviewPolicyPath))
				if err := os.MkdirAll(filepath.Dir(policy), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(policy, []byte(invalid), 0o644); err != nil {
					t.Fatal(err)
				}
				if source != "workspace" {
					runReviewCLIGit(t, repo, "add", reviewtransaction.RepositoryReviewPolicyPath)
					if err := os.Remove(policy); err != nil {
						t.Fatal(err)
					}
				}
				writeReviewStartCandidate(t, repo, "internal/unreviewed.go", "package unreviewed\n", 0o644)
				if result, err := mandatoryReviewValidate(t, repo, ""); err == nil || result.Allowed {
					t.Fatalf("invalid replacement escaped enforcement: %#v %v", result, err)
				}
			})
		}
	}
}

func TestMandatoryPolicyDiscoveryDamagedIndex(t *testing.T) {
	for _, source := range []string{"absent", "disabled", "HEAD", "workspace"} {
		t.Run(source, func(t *testing.T) {
			reviewEnabledHome(t)
			repo := initReviewCLIRepo(t)
			switch source {
			case "HEAD":
				enableMandatoryReviewPolicy(t, repo)
				if err := os.RemoveAll(filepath.Join(repo, ".gentle-ai")); err != nil {
					t.Fatal(err)
				}
			case "workspace", "disabled":
				policy := mandatoryReviewPolicyFixture
				if source == "disabled" {
					policy = strings.Replace(policy, ":true", ":false", 1)
				}
				writeReviewStartCandidate(t, repo, reviewtransaction.RepositoryReviewPolicyPath, policy, 0o644)
			}
			before, beforeErr := mandatoryReviewValidate(t, repo, "")
			if err := os.WriteFile(filepath.Join(repo, ".git", "index"), []byte("damaged-index\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			after, afterErr := mandatoryReviewValidate(t, repo, "")
			if source == "HEAD" || source == "workspace" {
				if afterErr == nil || after.Allowed {
					t.Fatalf("established mandatory policy ignored damaged index: %#v %v", after, afterErr)
				}
			} else if beforeErr != nil || afterErr != nil || before.Allowed || after.Allowed || before.Reason != after.Reason || before.Delivery != after.Delivery {
				t.Fatalf("no-policy informational behavior changed: %#v %v / %#v %v", before, beforeErr, after, afterErr)
			}
		})
	}
}

func TestMandatoryPolicyDiscoveryUnmergedIndex(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	policy := reviewtransaction.RepositoryReviewPolicyPath
	writeReviewStartCandidate(t, repo, policy, mandatoryReviewPolicyFixture, 0o644)
	blob := strings.TrimSpace(runReviewCLIGit(t, repo, "rev-parse", ":"+policy))
	runReviewCLIGit(t, repo, "update-index", "--force-remove", policy)
	runReviewCLIGitInput(t, repo, []byte("100644 "+blob+" 2\t"+policy+"\n100644 "+blob+" 3\t"+policy+"\n"), "update-index", "--index-info")
	if err := os.RemoveAll(filepath.Join(repo, ".gentle-ai")); err != nil {
		t.Fatal(err)
	}
	if result, err := mandatoryReviewValidate(t, repo, ""); err == nil || result.Allowed {
		t.Fatalf("unmerged enabled policy escaped enforcement: %#v %v", result, err)
	}
}

func TestMandatoryPolicyDiscoveryAlternateIndex(t *testing.T) {
	for _, scenario := range []struct {
		name, policy                                        string
		damagedDefault, damagedAlternate, enabled, rejected bool
	}{
		{name: "enabled alternate damaged default", policy: mandatoryReviewPolicyFixture, damagedDefault: true, enabled: true, rejected: true},
		{name: "enabled alternate healthy default", policy: mandatoryReviewPolicyFixture, enabled: true, rejected: true},
		{name: "policy free alternate healthy default"},
		{name: "policy free alternate unknown default", damagedDefault: true, rejected: true},
		{name: "malformed alternate policy", policy: "{", rejected: true},
		{name: "unreadable alternate", damagedAlternate: true, rejected: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reviewEnabledHome(t)
			repo := initReviewCLIRepo(t)
			alternate := filepath.Join(repo, ".git", "alternate-index")
			content, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(alternate, content, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_INDEX_FILE", alternate)
			if scenario.policy != "" {
				writeReviewStartCandidate(t, repo, reviewtransaction.RepositoryReviewPolicyPath, scenario.policy, 0o644)
				if err := os.RemoveAll(filepath.Join(repo, ".gentle-ai")); err != nil {
					t.Fatal(err)
				}
			}
			writeReviewStartCandidate(t, repo, "unreviewed.sh", "#!/bin/sh\nexit 0\n", 0o755)
			if scenario.damagedDefault {
				if err := os.WriteFile(filepath.Join(repo, ".git", "index"), []byte("damaged-index\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.damagedAlternate {
				if err := os.WriteFile(alternate, []byte("damaged-index\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			enabled, discoveryErr := (reviewtransaction.SnapshotBuilder{Repo: repo}).RepositoryMandatoryIsolatedReviewEnabled(t.Context())
			if scenario.enabled && (discoveryErr != nil || !enabled) {
				t.Errorf("alternate policy hidden: enabled=%v err=%v", enabled, discoveryErr)
			}
			if !scenario.rejected && discoveryErr != nil {
				t.Errorf("policy-free discovery failed: %v", discoveryErr)
			}
			result, err := mandatoryReviewValidate(t, repo, "")
			if (err != nil) != scenario.rejected || result.Allowed {
				t.Fatalf("unexpected alternate-index validation: %#v %v", result, err)
			}
			if scenario.enabled && (err == nil || !strings.Contains(err.Error(), "alternate GIT_INDEX_FILE")) {
				t.Fatalf("mandatory alternate-index operation did not reach identity rejection: %v", err)
			}
		})
	}
}
