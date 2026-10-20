package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewerprovider"
	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewtransaction"
)

const mandatoryReviewPolicyFixture = `{"schema":"gentle-ai.repository-review-policy/v1","mandatory_isolated_review":true}`

func enableMandatoryReviewPolicy(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, ".gentle-ai", "review-policy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(mandatoryReviewPolicyFixture+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "--", ".gentle-ai/review-policy.json")
	runReviewCLIGit(t, repo, "commit", "-qm", "require isolated review")
}

func mandatoryReviewValidate(t *testing.T, repo, lineage string) (ReviewValidateResult, error) {
	t.Helper()
	args := []string{"validate", "--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}
	if lineage != "" {
		args = append(args, "--lineage", lineage)
	}
	var output bytes.Buffer
	err := RunReview(args, &output)
	var result ReviewValidateResult
	if output.Len() > 0 {
		if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
			t.Fatalf("decode mandatory review validate result: %v\n%s", decodeErr, output.String())
		}
	}
	return result, err
}

func TestMandatoryReviewAssessmentMakesMediumChangesDue(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	base := strings.TrimSpace(runReviewCLIGit(t, repo, "rev-parse", "HEAD"))
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n\nfunc Enabled() bool { return true }\n", 0o644)
	runReviewCLIGit(t, repo, "commit", "-qm", "add code candidate")

	var output bytes.Buffer
	if err := RunReview([]string{"assess", "--cwd", repo, "--base-ref", base, "--committed-only", "--json"}, &output); err != nil {
		t.Fatalf("review assess: %v\n%s", err, output.String())
	}
	var schemaValue any
	if err := json.Unmarshal(output.Bytes(), &schemaValue); err != nil {
		t.Fatal(err)
	}
	if err := compileWholePublishedReviewSchema(t, "v2", "assess.schema.json").Validate(schemaValue); err != nil {
		t.Fatal(err)
	}
	var result ReviewAssessmentResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode mandatory assessment: %v\n%s", err, output.String())
	}
	if !result.ReviewDue || result.ReviewDueReason != "mandatory_isolated_review" || result.NextTransition == nil {
		t.Fatalf("mandatory code assessment = %#v, want review due independent of risk tier or slice budget", result)
	}
}

func TestMandatoryReviewPrePRRejectsEmptyWorkspaceCandidate(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)

	args := []string{"validate", "--cwd", repo, "--gate", string(reviewtransaction.GatePrePR)}
	var output bytes.Buffer
	err := RunReview(args, &output)
	var result ReviewValidateResult
	if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
		t.Fatalf("decode mandatory pre-pr result: %v\n%s", decodeErr, output.String())
	}
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("empty local workspace was treated as an approved PR candidate: result=%#v err=%v", result, err)
	}
}

func TestMandatoryReviewValidateRejectsUnreviewedAndRDDOffCandidates(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n\nfunc Enabled() bool { return true }\n", 0o644)
	if err := os.Remove(filepath.Join(repo, ".gentle-ai", "review-policy.json")); err != nil {
		t.Fatal(err)
	}

	result, err := mandatoryReviewValidate(t, repo, "")
	if result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("removing the policy and leaving a candidate unreviewed was treated as compliant: result=%#v err=%v", result, err)
	}

	if err := RunReviewMode([]string{"disable", "--scope", "global", "--cwd", repo}, io.Discard); err != nil {
		t.Fatalf("disable RDD: %v", err)
	}
	result, err = mandatoryReviewValidate(t, repo, "")
	if result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("RDD OFF was treated as mandatory-review compliance: result=%#v err=%v", result, err)
	}
}

func TestMandatoryReviewRejectsUnsupportedRuntimesAndManualStart(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n\nfunc Enabled() bool { return true }\n", 0o644)

	startAttempts := [][]string{
		{"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", string(model.AgentClaudeCode), "--lineage", "mandatory-claude"},
		{"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", string(model.AgentOpenCode), "--lineage", "mandatory-opencode"},
		{"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", string(model.AgentPi), "--lineage", "mandatory-pi"},
		{"start", "--cwd", repo, "--lineage", "mandatory-manual"},
	}
	for _, args := range startAttempts {
		var output bytes.Buffer
		if err := RunReview(args, &output); err == nil {
			t.Fatalf("unsupported mandatory start %q unexpectedly succeeded:\n%s", args, output.String())
		}
	}
	result, err := mandatoryReviewValidate(t, repo, "")
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("unsupported runtime attempts created compliance authority: result=%#v err=%v", result, err)
	}
}

func TestMandatoryReviewDeclinedConsentDoesNotSatisfyPolicy(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n\nfunc Enabled() bool { return true }\n", 0o644)

	questionArgs := boundNegotiatedStartArgs(t, []string{
		"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo,
		"--agent", string(model.AgentCodex), "--lineage", "mandatory-declined", "--consent", "relay",
	})
	question := decodeConsentQuestion(t, runConsentRelayStart(t, questionArgs).Bytes())
	decline := invocationArgs(t, question.Choices[1].Invocation)
	declined := runConsentRelayStart(t, decline)
	var declineResult ReviewFacadeStartResult
	decodeStrictReviewJSON(t, declined.Bytes(), &declineResult)
	if declineResult.Action != "declined" {
		t.Fatalf("mandatory candidate decline = %#v", declineResult)
	}

	result, err := mandatoryReviewValidate(t, repo, "")
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("declined consent was treated as compliance: result=%#v err=%v", result, err)
	}
}

func TestMandatoryReviewPassiveDocumentationRemainsExempt(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "docs/guide.md", "Ordinary passive documentation.\n", 0o644)

	var assess bytes.Buffer
	if err := RunReview([]string{"assess", "--cwd", repo, "--json"}, &assess); err != nil {
		t.Fatalf("assess passive documentation: %v\n%s", err, assess.String())
	}
	var assessment ReviewAssessmentResult
	if err := json.Unmarshal(assess.Bytes(), &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.Risk != "passive" || assessment.ReviewDue {
		t.Fatalf("passive documentation assessment = %#v, want exemption", assessment)
	}
	result, err := mandatoryReviewValidate(t, repo, "")
	if err != nil || !result.Allowed || result.Result != reviewtransaction.GateAllow {
		t.Fatalf("strictly passive documentation gate = %#v, %v; want allowed exemption", result, err)
	}
}

func TestMandatoryReviewDoesNotTreatGeneratedGoldenAsPassiveDocumentation(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature/testdata/golden/output.golden", "expected output\n", 0o644)

	var assess bytes.Buffer
	if err := RunReview([]string{"assess", "--cwd", repo, "--json"}, &assess); err != nil {
		t.Fatalf("assess generated golden: %v\n%s", err, assess.String())
	}
	var assessment ReviewAssessmentResult
	if err := json.Unmarshal(assess.Bytes(), &assessment); err != nil {
		t.Fatal(err)
	}
	if !assessment.ReviewDue || assessment.ReviewDueReason != "mandatory_isolated_review" {
		t.Fatalf("generated golden assessment = %#v, want mandatory review", assessment)
	}
	result, err := mandatoryReviewValidate(t, repo, "")
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("generated golden was treated as the passive documentation exemption: result=%#v err=%v", result, err)
	}
}

func TestMandatoryReviewDoesNotExemptContractDocumentation(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "contracts/service.md", "This document defines the service contract.\n", 0o644)

	var assess bytes.Buffer
	if err := RunReview([]string{"assess", "--cwd", repo, "--json"}, &assess); err != nil {
		t.Fatalf("assess contract documentation: %v\n%s", err, assess.String())
	}
	var assessment ReviewAssessmentResult
	if err := json.Unmarshal(assess.Bytes(), &assessment); err != nil {
		t.Fatal(err)
	}
	if !assessment.ReviewDue || assessment.ReviewDueReason != "mandatory_isolated_review" {
		t.Fatalf("contract document assessment = %#v, want mandatory review", assessment)
	}
	result, err := mandatoryReviewValidate(t, repo, "")
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("contract documentation was treated as passive prose: result=%#v err=%v", result, err)
	}
}

func TestMandatoryReviewRejectsIncompleteAndManualCodexCaptures(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n\nfunc Enabled() bool { return true }\n", 0o644)
	started := startFacadeReviewForRuntime(t, repo, model.AgentCodex)
	result, err := mandatoryReviewValidate(t, repo, started.LineageID)
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("incomplete Codex review was treated as compliant: result=%#v err=%v", result, err)
	}

	store, record, err := discoverCompactFacadeReview(t.Context(), repo, started.LineageID, false)
	if err != nil {
		t.Fatal(err)
	}
	lens := record.State.SelectedLenses[0]
	frozen, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).FrozenCandidateContext(t.Context(), record.State.InitialSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := reviewtransaction.NewArtifactSubject(record.State, record.State.CapturePhaseRevision, frozen, lens, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(frozen.ChangedPathManifest))
	for index, entry := range frozen.ChangedPathManifest {
		paths[index] = entry.Path
	}
	payload, err := json.Marshal(facadeReviewerResult{
		SubjectHash: subject.SubjectHash,
		Inspection:  reviewtransaction.ArtifactInspection{Status: reviewtransaction.ArtifactInspectionCompleted, Paths: paths},
		Findings:    []facadeFinding{}, Evidence: []string{"inspected the frozen candidate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := writeReviewCLIRawInput(t, payload)
	err = RunReviewCaptureResult([]string{
		"--cwd", repo, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity,
		"--lens", lens, "--order", "0", "--input", input,
	}, io.Discard)
	if err == nil {
		current, loadErr := store.Load()
		t.Fatalf("mandatory Codex review accepted host-supplied output: record=%#v loadErr=%v", current.State, loadErr)
	}
}

func TestMandatoryReviewMockCodexLocalHandoffLifecycle(t *testing.T) {
	for _, projection := range []string{"staged", "workspace"} {
		t.Run(projection, func(t *testing.T) { mandatoryReviewMockCodexLocalHandoff(t, projection) })
	}
}

func mandatoryReviewMockCodexLocalHandoff(t *testing.T, projection string) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n\nfunc Enabled() bool { return true }\n", 0o644)
	var startOutput bytes.Buffer
	args := boundNegotiatedStartArgs(t, []string{"start", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "codex", "--projection", projection, "--lineage", "mandatory-local-handoff", "--consent", "granted"})
	if err := RunReview(args, &startOutput); err != nil {
		t.Fatalf("START: %v %s", err, &startOutput)
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(startOutput.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	validate := func() (ReviewValidateResult, error) { return mandatoryReviewValidate(t, repo, started.LineageID) }
	store, record, err := discoverCompactFacadeReview(t.Context(), repo, started.LineageID, false)
	if err != nil {
		t.Fatal(err)
	}
	root, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).ResolveRepositoryRoot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	handle, err := reviewtransaction.DeriveReviewRepositoryContextHandle(t.Context(), root, reviewtransaction.ReviewRepositoryContextBinding{
		LineageID: record.State.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.State.CapturePhaseRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mock only: providerTestAdapter returns deterministic bytes without a model call.
	previous := reviewProviderAdapterFor
	t.Cleanup(func() { reviewProviderAdapterFor = previous })

	for order, lens := range record.State.SelectedLenses {
		payload, marshalErr := json.Marshal(admittedReviewerResultForTest(t, repo, record, lens, order))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		reviewProviderAdapterFor = func(_ reviewerprovider.Contract, agent model.AgentID) (reviewerprovider.Adapter, error) {
			if agent != model.AgentCodex {
				return nil, errors.New("mandatory review selected a non-Codex adapter")
			}
			return providerTestAdapter{raw: payload}, nil
		}
		var output bytes.Buffer
		if err := RunReviewCaptureResult([]string{
			"--cwd", repo, "--repository-context", handle, "--lineage", started.LineageID,
			"--target", record.State.InitialSnapshot.Identity, "--expected-revision", record.State.CapturePhaseRevision,
			"--lens", lens, "--order", fmt.Sprint(order), "--agent", string(model.AgentCodex),
		}, &output); err != nil {
			t.Fatalf("execute isolated Codex lens %q: %v\n%s", lens, err, output.String())
		}
		if order+1 == len(record.State.SelectedLenses) {
			var terminal reviewLastEventClosureResult
			if err := json.Unmarshal(output.Bytes(), &terminal); err != nil {
				t.Fatal(err)
			}
			if terminal.Acknowledgement != nil || terminal.StatusContinuation == nil || terminal.StatusContinuation.Operation != "review.validate" {
				t.Fatalf("approval did not retain authority for handoff: %#v", terminal)
			}
			var value any
			_ = json.Unmarshal(output.Bytes(), &value)
			if err := compileWholePublishedReviewSchema(t, "v2", "last-event-closure.schema.json").Validate(value); err != nil {
				t.Fatal(err)
			}
		}
		if order+1 < len(record.State.SelectedLenses) {
			record, err = store.Load()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	approved, err := store.Load()
	if err != nil || approved.State.State != reviewtransaction.StateApproved {
		t.Fatalf("Codex review did not approve: state=%q err=%v", approved.State.State, err)
	}
	if _, pending := reviewtransaction.PendingApprovedCompactAcknowledgement(approved); !pending {
		t.Fatal("successful mandatory review did not retain the existing pending acknowledgement")
	}

	transport, err := store.ExportTransport()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(transport)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunReviewBundleImport([]string{"--cwd", repo, "--bundle", writeReviewCLIRawInput(t, payload)}, io.Discard); err == nil {
		t.Fatal("manual bundle import satisfied mandatory review")
	}
	var statusOutput bytes.Buffer
	if err := RunReview([]string{"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--lineage", started.LineageID, "--projection", projection, "--next-transition"}, &statusOutput); err != nil {
		t.Fatalf("STATUS: %v %s", err, &statusOutput)
	}
	var status ReviewTargetStatusResult
	if err := json.Unmarshal(statusOutput.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.NextTransition == nil || status.NextTransition.Execute == nil || status.NextTransition.Execute.Operation != "review.validate" {
		t.Fatalf("STATUS offered premature burn: %s", &statusOutput)
	}
	runTransitionTokens(t, "validate", status.NextTransition.Execute.Arguments)
	result, err := validate()
	if err != nil || !result.Allowed || result.Result != reviewtransaction.GateAllow {
		t.Fatalf("active Codex approval did not satisfy mandatory review: result=%#v err=%v", result, err)
	}
	if result.Next == nil || result.Next.ReasonCode != "local_review_handoff" || !strings.Contains(result.Reason, "not durable authorization") {
		t.Fatalf("handoff semantics missing: %#v", result)
	}
	if afterHandoff, loadErr := store.Load(); loadErr != nil || afterHandoff.Revision != approved.Revision {
		t.Fatalf("handoff mutated approval authority: %v", loadErr)
	}
	ack, _ := reviewtransaction.PendingApprovedCompactAcknowledgement(approved)
	if result.Next.Transition != reviewApprovedAcknowledgementTransition(repo, ack).Command {
		t.Fatal("handoff did not return exact existing acknowledgement")
	}
	if err := RunReviewMode([]string{"disable", "--scope", "global", "--cwd", repo}, io.Discard); err != nil {
		t.Fatalf("disable RDD with an active approval: %v", err)
	}
	result, err = validate()
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("RDD OFF was treated as compliance despite an active Codex approval: result=%#v err=%v", result, err)
	}
	if err := RunReviewMode([]string{"enable", "--scope", "global", "--cwd", repo}, io.Discard); err != nil {
		t.Fatalf("re-enable RDD for candidate mutation checks: %v", err)
	}
	result, err = validate()
	if err != nil || !result.Allowed {
		t.Fatalf("restoring RDD did not restore compliance with the active approval: result=%#v err=%v", result, err)
	}

	featurePath := filepath.Join(repo, "internal", "feature.go")
	if err := os.WriteFile(featurePath, []byte("package feature\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "internal/feature.go")
	result, err = validate()
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("candidate mutation after approval remained compliant: result=%#v err=%v", result, err)
	}
	if err := os.WriteFile(featurePath, []byte("package feature\n\nfunc Enabled() bool { return true }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "internal/feature.go")
	result, err = validate()
	if err != nil || !result.Allowed {
		t.Fatalf("restoring the reviewed candidate did not restore compliance before acknowledgement: result=%#v err=%v", result, err)
	}
	t.Run("default-index-environment", func(t *testing.T) {
		t.Setenv("GIT_INDEX_FILE", ".git/index")
		if result, err := validate(); err != nil || !result.Allowed {
			t.Fatalf("default index rejected: %#v %v", result, err)
		}
	})
	t.Run("alternate-index", func(t *testing.T) {
		t.Setenv("GIT_INDEX_FILE", filepath.Join(repo, ".git", "other-index"))
		if result, err := validate(); err == nil || result.Allowed {
			t.Fatalf("alternate index accepted: %#v %v", result, err)
		}
	})
	t.Run("reviewed-workspace-unreviewed-index", func(t *testing.T) {
		writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\nfunc Unreviewed() {}\n", 0o644)
		runReviewCLIGit(t, repo, "add", "internal/feature.go")
		if err := os.WriteFile(featurePath, []byte("package feature\n\nfunc Enabled() bool { return true }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if result, err := validate(); err == nil || result.Allowed {
			t.Fatalf("unreviewed index accepted: %#v %v", result, err)
		}
		runReviewCLIGit(t, repo, "add", "internal/feature.go")
	})

	acknowledgement, pending := reviewtransaction.PendingApprovedCompactAcknowledgement(approved)
	if !pending {
		t.Fatal("approved authority has no pending acknowledgement to burn")
	}
	if err := RunReview([]string{
		"acknowledge-approved", "--cwd", repo, "--lineage", acknowledgement.LineageID,
		"--target", acknowledgement.TargetIdentity, "--expected-revision", acknowledgement.ExpectedRevision,
		"--token", acknowledgement.Token,
	}, io.Discard); err != nil {
		t.Fatalf("burn approved authority through existing acknowledgement: %v", err)
	}
	if _, loadErr := store.Load(); !os.IsNotExist(loadErr) {
		t.Fatalf("acknowledgement did not remove authority: %v", loadErr)
	}
	result, err = validate()
	if err == nil || result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("burned local authority remained mandatory-review compliance: result=%#v err=%v", result, err)
	}
	for _, gate := range []string{"pre-push", "pre-pr", "release"} {
		var output bytes.Buffer
		err := RunReview([]string{"validate", "--cwd", repo, "--gate", gate, "--base-ref", "HEAD"}, &output)
		var published ReviewValidateResult
		_ = json.Unmarshal(output.Bytes(), &published)
		if err == nil || published.Allowed || !strings.Contains(published.Reason, "unverified") {
			t.Fatalf("burn became publication authority: %s %v", &output, err)
		}
	}
}

func TestAuditMandatoryRefuterRuntimeInjection(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\nfunc Enabled() bool {return true}\n", 0o644)
	started := startFacadeReviewForRuntime(t, repo, model.AgentCodex)
	store, record, err := discoverCompactFacadeReview(t.Context(), repo, started.LineageID, false)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := reviewtransaction.DeriveReviewRepositoryContextHandle(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{LineageID: record.State.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.State.CapturePhaseRevision})
	if err != nil {
		t.Fatal(err)
	}
	previous := reviewProviderAdapterFor
	t.Cleanup(func() { reviewProviderAdapterFor = previous })
	for order, lens := range record.State.SelectedLenses {
		result := admittedReviewerResultForTest(t, repo, record, lens, order)
		if order == 0 {
			result.Findings = []facadeFinding{{ID: "R3-001", Location: "internal/feature.go:2", Severity: "CRITICAL", Claim: "candidate failure", ProofRefs: []string{"internal/feature.go:2 candidate-specific proof"}, EvidenceClass: reviewtransaction.EvidenceInferential, CausalDisposition: reviewtransaction.CausalBehaviorActivated}}
		}
		payload, _ := json.Marshal(result)
		reviewProviderAdapterFor = func(contract reviewerprovider.Contract, agent model.AgentID) (reviewerprovider.Adapter, error) {
			if contract.Role == reviewerprovider.RoleRefuter {
				return nil, errors.New("audit: no refuter model permitted")
			}
			return providerTestAdapter{raw: payload}, nil
		}
		var out bytes.Buffer
		captureErr := RunReviewCaptureResult([]string{"--cwd", repo, "--repository-context", handle, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity, "--expected-revision", record.State.CapturePhaseRevision, "--lens", lens, "--order", fmt.Sprint(order), "--agent", "codex"}, &out)
		t.Logf("lens %s capture error: %v", lens, captureErr)
		record, err = store.Load()
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := reviewProviderNewRefuterRequest(t.Context(), repo, store.Dir, record.State, record.State.CapturePhaseRevision)
	if err != nil {
		t.Fatal(err)
	}
	results := []facadeRefuterOutcome{}
	for _, claim := range request.Claims {
		results = append(results, facadeRefuterOutcome{FindingID: claim.FindingID, Outcome: reviewtransaction.OutcomeRefuted, ProofRefs: []string{"manually authored audit refutation"}})
	}
	raw, _ := json.Marshal(facadeRefuterResult{RequestHash: request.RequestHash, Results: results})
	input := writeReviewCLIRawInput(t, raw)
	var out bytes.Buffer
	err = RunReviewCaptureRefuter([]string{"--cwd", repo, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity, "--expected-revision", record.State.CapturePhaseRevision, "--agent", "pi", "--input", input}, &out)
	if err == nil {
		t.Fatal("mandatory Codex lineage accepted a manually supplied Pi refutation")
	}
	current, loadErr := store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if current.Revision != record.Revision {
		t.Fatal("rejected refuter changed authority")
	}
}

func TestMandatoryReviewRoleTransportIsFrozen(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	enableMandatoryReviewPolicy(t, repo)
	writeReviewStartCandidate(t, repo, "internal/feature.go", "package feature\n", 0o644)
	started := startFacadeReviewForRuntime(t, repo, model.AgentCodex)
	_, record, err := discoverCompactFacadeReview(t.Context(), repo, started.LineageID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"capture-refuter", "capture-validation"} {
		args := []string{role, "--cwd", repo, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity, "--expected-revision", record.State.CapturePhaseRevision, "--agent", "pi", "--input", writeReviewCLIRawInput(t, []byte("{}"))}
		if role == "capture-validation" {
			args = append(args, "--request-hash", "audit")
		}
		err := RunReview(args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "mandatory isolated review") {
			t.Fatalf("%s did not enforce mandatory provenance: %v", role, err)
		}
	}
	handle, err := reviewtransaction.DeriveOpenCodeReviewRepositoryContextHandle(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{LineageID: started.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.State.CapturePhaseRevision})
	if err != nil {
		t.Fatal(err)
	}
	task, err := newReviewProviderTask(reviewerprovider.RoleRefuter, ReviewTransitionBinding{LineageID: started.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.State.CapturePhaseRevision, RepositoryContext: handle})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openCodeTransportStartBound(t.Context(), task.Prompt, "review-refuter"); err == nil || !strings.Contains(err.Error(), "mandatory isolated review") {
		t.Fatalf("OpenCode relay bypassed provenance: %v", err)
	}

}

func TestMandatoryReviewNoPolicySymlinkCompatibility(t *testing.T) {
	reviewEnabledHome(t)
	repo := initReviewCLIRepo(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(repo, ".gentle-ai")); err != nil {
		t.Fatal(err)
	}
	if err := RunReview([]string{"validate", "--cwd", repo, "--gate", "pre-commit"}, io.Discard); err != nil {
		t.Fatalf("no-policy upstream behavior regressed: %v", err)
	}
}
