package gov

import (
	"fmt"
	"time"

	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/gov/keeper"
	"github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
)

// EndBlocker called every block, process inflation, update validator set.
func EndBlocker(ctx sdk.Context, keeper keeper.Keeper) {
	defer telemetry.ModuleMeasureSince(v1beta1.ModuleName, time.Now(), telemetry.MetricKeyEndBlocker)

	logger := keeper.Logger(ctx)

	// delete dead proposals from store and burn theirs deposits. A proposal is dead when it's inactive and didn't get enough deposit on time to get into voting phase.
	keeper.IterateInactiveProposalsQueue(ctx, ctx.BlockHeader().Time, func(proposal v1beta1.Proposal) bool {
		keeper.DeleteProposal(ctx, proposal.ProposalId)
		keeper.DeleteAndBurnDeposits(ctx, proposal.ProposalId)

		// called when proposal become inactive
		keeper.AfterProposalFailedMinDeposit(ctx, proposal.ProposalId)

		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				types.EventTypeInactiveProposal,
				sdk.NewAttribute(types.AttributeKeyProposalID, fmt.Sprintf("%d", proposal.ProposalId)),
				sdk.NewAttribute(types.AttributeKeyProposalResult, types.AttributeValueProposalDropped),
			),
		)

		logger.Info(
			"proposal did not meet minimum deposit; deleted",
			"proposal", proposal.ProposalId,
			"title", proposal.GetTitle(),
			"min_deposit", keeper.GetDepositParams(ctx).MinDeposit.String(),
			"total_deposit", proposal.TotalDeposit.String(),
		)

		return false
	})

	// fetch active proposals whose voting periods have ended (are passed the block time)
	keeper.IterateActiveProposalsQueue(ctx, ctx.BlockHeader().Time, func(proposal v1beta1.Proposal) bool {
		var tagValue, logMsg string

		passes, burnDeposits, tallyResults := keeper.Tally(ctx, proposal)

		if burnDeposits {
			keeper.DeleteAndBurnDeposits(ctx, proposal.ProposalId)
		} else {
			keeper.RefundAndDeleteDeposits(ctx, proposal.ProposalId)
		}

		if passes {
			handler := keeper.Router().GetRoute(proposal.ProposalRoute())
			cacheCtx, writeCache := ctx.CacheContext()

			// The proposal handler may execute state mutating logic depending
			// on the proposal content. If the handler fails, no state mutation
			// is written and the error message is logged.
			err := handler(cacheCtx, proposal.GetContent())
			if err == nil {
				proposal.Status = v1beta1.StatusPassed
				tagValue = types.AttributeValueProposalPassed
				logMsg = "passed"

				// The cached context is created with a new EventManager. However, since
				// the proposal handler execution was successful, we want to track/keep
				// any events emitted, so we re-emit to "merge" the events into the
				// original Context's EventManager.
				ctx.EventManager().EmitEvents(cacheCtx.EventManager().Events())

				// write state to the underlying multi-store
				writeCache()
			} else {
				proposal.Status = v1beta1.StatusFailed
				tagValue = types.AttributeValueProposalFailed
				logMsg = fmt.Sprintf("passed, but failed on execution: %s", err)
			}
		} else {
			proposal.Status = v1beta1.StatusRejected
			tagValue = types.AttributeValueProposalRejected
			logMsg = "rejected"
		}

		proposal.FinalTallyResult = tallyResults

		keeper.SetProposal(ctx, proposal)
		keeper.RemoveFromActiveProposalQueue(ctx, proposal.ProposalId, proposal.VotingEndTime)

		// when proposal become active
		keeper.AfterProposalVotingPeriodEnded(ctx, proposal.ProposalId)

		logger.Info(
			"proposal tallied",
			"proposal", proposal.ProposalId,
			"title", proposal.GetTitle(),
			"result", logMsg,
		)

		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				types.EventTypeActiveProposal,
				sdk.NewAttribute(types.AttributeKeyProposalID, fmt.Sprintf("%d", proposal.ProposalId)),
				sdk.NewAttribute(types.AttributeKeyProposalResult, tagValue),
			),
		)
		return false
	})
}
