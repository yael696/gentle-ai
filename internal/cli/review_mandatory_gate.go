package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/reviewtransaction"
)

// All capture roles use the frozen policy and the same runtime/transport check.
func validateMandatoryReviewCapture(ctx context.Context, root string, state reviewtransaction.CompactState, runtime model.AgentID, rawInput, execution, readOnly bool) error {
	builder := reviewtransaction.SnapshotBuilder{Repo: root}
	frozen, err := builder.SnapshotRequiresMandatoryIsolatedReview(ctx, state.InitialSnapshot)
	if err != nil {
		return reviewPreflightError(err)
	}
	current, err := builder.RepositoryMandatoryIsolatedReviewEnabled(ctx)
	if err != nil {
		return reviewPreflightError(err)
	}
	if current && !frozen {
		return reviewPreflightRefusal(reviewImmutableTransportUnsupportedReason, errors.New("mandatory isolated review was enabled after this lineage started; start a fresh Codex review for the current candidate"))
	}
	if frozen || current {
		if state.RuntimeAgent != string(model.AgentCodex) || rawInput || (!readOnly && (!execution || runtime != model.AgentCodex)) || (runtime != "" && runtime != model.AgentCodex) {
			return reviewPreflightRefusal(reviewImmutableTransportUnsupportedReason, errors.New("mandatory isolated review requires the frozen Codex runtime and executing its adapter without --input; start a fresh Codex review or refresh the native next_transition"))
		}
	}
	return nil
}

func mandatoryReviewGateSnapshot(ctx context.Context, root string) (reviewtransaction.Snapshot, error) {
	builder := reviewtransaction.SnapshotBuilder{Repo: root}
	if err := builder.ValidateDefaultCommitIndex(ctx); err != nil {
		return reviewtransaction.Snapshot{}, err
	}
	return builder.Build(ctx, reviewtransaction.Target{Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionStaged, IntendedUntracked: []string{}})
}

func runMandatoryIsolatedReviewGate(
	ctx context.Context,
	root string,
	gate reviewtransaction.GateKind,
	lineage string,
	snapshot reviewtransaction.Snapshot,
	negotiated bool,
	contract string,
	stdout io.Writer,
) error {
	builder := reviewtransaction.SnapshotBuilder{Repo: root}
	if len(snapshot.Paths) == 0 {
		if err := builder.ValidateLiveSnapshot(ctx, snapshot); err != nil {
			return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("candidate changed during mandatory review validation: %w", err))
		}
		return mandatoryReviewGateAllow(root, stdout, negotiated, contract, gate, snapshot, nil, "there are no current changes to review")
	}
	strictlyPassiveDocumentation, err := builder.SnapshotIsStrictlyPassiveDocumentation(ctx, snapshot)
	if err != nil {
		return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("prove passive documentation exemption: %w", err))
	}
	if strictlyPassiveDocumentation {
		if err := builder.ValidateLiveSnapshot(ctx, snapshot); err != nil {
			return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("candidate changed during mandatory review validation: %w", err))
		}
		return mandatoryReviewGateAllow(root, stdout, negotiated, contract, gate, snapshot, nil, "strictly passive documentation is exempt from mandatory review")
	}
	disabled, err := reviewDrivenDevelopmentDisabled(ctx, root)
	if err != nil {
		return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("resolve RDD mode: %w", err))
	}
	if disabled {
		return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot,
			errors.New("RDD is disabled; mandatory isolated review cannot be satisfied while RDD is off"))
	}

	var stores []reviewtransaction.CompactStore
	if lineage != "" {
		store, storeErr := reviewtransaction.CompactAuthoritativeStore(ctx, root, lineage)
		if storeErr != nil {
			return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("resolve compact review lineage: %w", storeErr))
		}
		stores = []reviewtransaction.CompactStore{store}
	} else {
		stores, err = reviewtransaction.DiscoverCompactStores(ctx, root)
		if err != nil {
			return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("discover compact review authorities: %w", err))
		}
	}
	type match struct {
		record reviewtransaction.CompactRecord
	}
	matches := []match{}
	for _, store := range stores {
		record, loadErr := store.LoadContext(ctx)
		if loadErr != nil {
			return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("load compact review authority: %w", loadErr))
		}
		state := record.State
		if state.State != reviewtransaction.StateApproved || len(state.SelectedLenses) == 0 || state.RuntimeAgent != string(model.AgentCodex) {
			continue
		}
		if _, pending := reviewtransaction.PendingApprovedCompactAcknowledgement(record); !pending {
			continue
		}
		// Frozen policy proves every contributing capture was subject to the
		// mandatory transport guard; RuntimeAgent alone does not prove this.
		frozenMandatory, policyErr := builder.SnapshotRequiresMandatoryIsolatedReview(ctx, state.InitialSnapshot)
		if policyErr != nil {
			return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, policyErr)
		}
		if !frozenMandatory {
			continue
		}
		if !compactAuthorityMatchesMandatoryCandidate(state, snapshot) {
			continue
		}
		view, viewErr := state.CompactReviewView()
		if viewErr != nil || len(view.LensResults) != len(state.SelectedLenses) {
			continue
		}
		matches = append(matches, match{record: record})
	}
	if len(matches) != 1 {
		message := "no active, complete Codex review authority matches the current candidate"
		if len(matches) > 1 {
			message = "multiple active Codex review authorities match the current candidate; specify one --lineage"
		}
		if lineage != "" && len(matches) == 0 {
			message = fmt.Sprintf("lineage %q is incomplete, non-Codex, burned, or does not match the current candidate", lineage)
		}
		return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, errors.New(message))
	}
	if err := builder.ValidateLiveSnapshot(ctx, snapshot); err != nil {
		return mandatoryReviewGateFailure(stdout, negotiated, contract, gate, snapshot, fmt.Errorf("candidate changed after approval: %w", err))
	}
	return mandatoryReviewGateAllow(root, stdout, negotiated, contract, gate, snapshot, &matches[0].record,
		"local review handoff for the exact staged candidate; this is not durable authorization of a future commit or publication")
}

// Snapshot identities remain projection-specific. A local handoff compares
// immutable content: the entire reviewed base and candidate trees must match
// the actual staged candidate. It does not authorize an eventual commit.
func compactAuthorityMatchesMandatoryCandidate(state reviewtransaction.CompactState, live reviewtransaction.Snapshot) bool {
	return state.InitialSnapshot.BaseTree == live.BaseTree && state.CurrentSnapshot.CandidateTree == live.CandidateTree
}

func mandatoryReviewGateContext(gate reviewtransaction.GateKind, snapshot reviewtransaction.Snapshot, record *reviewtransaction.CompactRecord) reviewtransaction.GateContext {
	context := reviewtransaction.GateContext{
		Gate: gate, BaseTree: snapshot.BaseTree, CandidateTree: snapshot.CandidateTree, PathsDigest: snapshot.PathsDigest,
		BaseRelationshipValid: record != nil,
	}
	if record != nil {
		state := record.State
		context.LineageID = state.LineageID
		context.Generation = state.Generation
		context.StoreRevision = record.Revision
		context.BaseTree = state.InitialSnapshot.BaseTree
		context.CandidateTree = state.CurrentSnapshot.CandidateTree
		context.PathsDigest = state.CurrentSnapshot.PathsDigest
		context.FixDeltaHash = state.FixDeltaHash
		context.PolicyHash = state.PolicyHash
		context.EvidenceHash = state.EvidenceHash
	}
	return context
}

func mandatoryReviewGateAllow(
	root string,
	stdout io.Writer,
	negotiated bool,
	contract string,
	gate reviewtransaction.GateKind,
	snapshot reviewtransaction.Snapshot,
	record *reviewtransaction.CompactRecord,
	reason string,
) error {
	result := ReviewValidateResult{
		Schema: ReviewValidateSchema, Result: reviewtransaction.GateAllow, Allowed: true,
		Action: "continue", Reason: reason, Context: mandatoryReviewGateContext(gate, snapshot, record),
	}
	if record != nil {
		ack, _ := reviewtransaction.PendingApprovedCompactAcknowledgement(*record)
		result.Next = &reviewtransaction.GateNextStep{ReasonCode: "local_review_handoff", Transition: reviewApprovedAcknowledgementTransition(root, ack).Command}
	}
	return encodeReviewIntegrationOperation(stdout, negotiated, ReviewIntegrationOperationValidate, result, result, contract)
}

func mandatoryReviewGateFailure(
	stdout io.Writer,
	negotiated bool,
	contract string,
	gate reviewtransaction.GateKind,
	snapshot reviewtransaction.Snapshot,
	cause error,
) error {
	result := ReviewValidateResult{
		Schema: ReviewValidateSchema, Result: reviewtransaction.GateInvalidated, Allowed: false,
		Action: "review.status", Reason: cause.Error(), Context: mandatoryReviewGateContext(gate, snapshot, nil),
	}
	if gate != reviewtransaction.GatePreCommit {
		result.Action = reviewDeliveryPolicyAction
		result.Next = &reviewtransaction.GateNextStep{ReasonCode: "publication_boundary_unverified"}
	}
	if err := encodeReviewIntegrationOperation(stdout, negotiated, ReviewIntegrationOperationValidate, result, result, contract); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("mandatory isolated review: %w", cause)
}

func reviewApprovedContinuation(ctx context.Context, root string, state reviewtransaction.CompactState, ack reviewtransaction.ApprovedCompactAcknowledgement) (*ReviewTransitionExecution, error) {
	builder := reviewtransaction.SnapshotBuilder{Repo: root}
	current, err := builder.RepositoryMandatoryIsolatedReviewEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !current || len(state.SelectedLenses) == 0 {
		return reviewApprovedAcknowledgementTransition(root, ack), nil
	}
	return reviewExecuteTransition("local_review_handoff_required", "review.validate", []ReviewTransitionArgument{
		{Name: "cwd", Value: root}, {Name: "contract", Value: ReviewIntegrationContractV2},
		{Name: "gate", Value: string(reviewtransaction.GatePreCommit)}, {Name: "lineage", Value: ack.LineageID},
	}, []ReviewTransitionArgument{{Name: "state", Value: string(reviewtransaction.StateApproved)}}, ReviewTransitionBinding{LineageID: ack.LineageID, TargetIdentity: ack.TargetIdentity, Revision: ack.ExpectedRevision}, nil).Execute, nil
}
