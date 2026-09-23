use anchor_lang::prelude::*;

declare_id!("AlphaNetX11111111111111111111111111111111111");

/// Alpha Network Solana Program
/// 
/// This program implements the on-chain layer of Alpha Network on Solana:
/// - Agent registration and identity
/// - $ALPHA staking for validators
/// - Reputation tracking
/// - Reward distribution
/// - PoI proof verification
#[program]
pub mod alpha_network {
    use super::*;

    // ─── Errors ────────────────────────────────────────────────────────────

    #[error_code]
    pub enum AlphaError {
        #[msg("Insufficient stake amount")]
        InsufficientStake,
        #[msg("Agent already registered")]
        AgentAlreadyRegistered,
        #[msg("Agent not registered")]
        AgentNotRegistered,
        #[msg("Invalid agent address")]
        InvalidAgentAddress,
        #[msg("Stake period not elapsed")]
        StakePeriodNotElapsed,
        #[msg("Invalid capability")]
        InvalidCapability,
        #[msg("Reward already claimed")]
        RewardAlreadyClaimed,
        #[msg("Slashing condition met")]
        SlashingCondition,
    }

    // ─── Constants ─────────────────────────────────────────────────────────

    /// Minimum stake required to register as a validator (1,000 ALPHA)
    pub const MIN_STAKE: u64 = 1_000_000_000_000; // 1000 ALPHA * 10^9 decimals

    /// Block reward per epoch (decayed over time)
    pub const BASE_BLOCK_REWARD: u64 = 6_337_000_000_000; // 6337 ALPHA

    /// Slash penalty percentage (10%)
    pub const SLASH_PENALTY_BPS: u64 = 1000; // 1000 basis points = 10%

    /// Maximum number of capabilities per agent
    pub const MAX_CAPABILITIES: usize = 8;

    // ─── Registration ──────────────────────────────────────────────────────

    /// Register a new AI agent on Alpha Network
    /// 
    /// # Arguments
    /// * `capabilities` - Vector of capability strings the agent supports
    /// * `stake_amount` - Amount of $ALPHA to stake (minimum 1,000)
    /// 
    /// # Accounts
    /// * `agent` - The AI agent's wallet (signer)
    /// * `agent_account` - On-chain agent record (created)
    /// * `stake_account` - Token account holding the staked ALPHA
    /// * `token_program` - SPL Token program
    pub fn register_agent(
        ctx: Context<RegisterAgent>,
        capabilities: Vec<String>,
        stake_amount: u64,
    ) -> Result<()> {
        let agent = &mut ctx.accounts.agent_account;

        // Validate stake amount
        require!(
            stake_amount >= MIN_STAKE,
            AlphaError::InsufficientStake
        );

        // Validate capabilities
        require!(
            capabilities.len() <= MAX_CAPABILITIES,
            AlphaError::InvalidCapability
        );

        // Initialize agent record
        agent.agent_address = ctx.accounts.agent.key();
        agent.capabilities = capabilities;
        agent.stake_amount = stake_amount;
        agent.reputation_score = 100; // Start with neutral reputation
        agent.task_count = 0;
        agent.total_earned = 0;
        agent.total_burned = 0;
        agent.registered_slot = Clock::get()?.slot;
        agent.last_active_slot = Clock::get()?.slot;
        agent.is_active = true;

        msg!("✅ Agent registered: {}", agent.agent_address);
        msg!("   Stake: {} ALPHA", stake_amount / 1_000_000_000);
        msg!("   Capabilities: {:?}", agent.capabilities);

        Ok(())
    }

    // ─── Staking ───────────────────────────────────────────────────────────

    /// Increase stake amount for an existing agent
    pub fn increase_stake(
        ctx: Context<StakeUpdate>,
        amount: u64,
    ) -> Result<()> {
        let agent = &mut ctx.accounts.agent_account;
        require!(agent.is_active, AlphaError::AgentNotRegistered);

        agent.stake_amount = agent.stake_amount.checked_add(amount)
            .ok_or(AlphaError::InsufficientStake)?;
        agent.last_active_slot = Clock::get()?.slot;

        msg!("✅ Stake increased: {} total ALPHA", agent.stake_amount / 1_000_000_000);

        Ok(())
    }

    /// Unstake tokens (subject to unbonding period)
    pub fn unstake(
        ctx: Context<StakeUpdate>,
        amount: u64,
    ) -> Result<()> {
        let agent = &mut ctx.accounts.agent_account;
        require!(agent.is_active, AlphaError::AgentNotRegistered);
        require!(amount <= agent.stake_amount, AlphaError::InsufficientStake);

        // Ensure minimum stake remains
        require!(
            agent.stake_amount.checked_sub(amount).unwrap_or(0) >= MIN_STAKE || amount == agent.stake_amount,
            AlphaError::InsufficientStake
        );

        agent.stake_amount = agent.stake_amount.checked_sub(amount)
            .ok_or(AlphaError::InsufficientStake)?;
        agent.last_active_slot = Clock::get()?.slot;

        // TODO: Implement unbonding queue for security

        msg!("✅ Unstaked: {} ALPHA", amount / 1_000_000_000);

        Ok(())
    }

    // ─── Rewards ───────────────────────────────────────────────────────────

    /// Claim accumulated block rewards
    /// 
    /// Rewards are calculated based on:
    /// - Stake amount (more stake = more rewards)
    /// - Reputation score (higher reputation = bonus multiplier)
    /// - Task completion (bonus for completing marketplace tasks)
    pub fn claim_rewards(ctx: Context<ClaimRewards>) -> Result<()> {
        let agent = &mut ctx.accounts.agent_account;
        require!(agent.is_active, AlphaError::AgentNotRegistered);

        let clock = Clock::get()?;
        let slots_since_last = clock.slot.checked_sub(agent.last_active_slot)
            .unwrap_or(0);

        // Calculate rewards
        let base_reward = calculate_block_reward(agent.stake_amount, slots_since_last);
        let reputation_multiplier = agent.reputation_score as u64 * 100; // 100 = 1.0x
        let total_reward = base_reward
            .checked_mul(reputation_multiplier)
            .unwrap_or(0)
            .checked_div(10000)
            .unwrap_or(0);

        // Update agent state
        agent.total_earned = agent.total_earned.checked_add(total_reward)
            .unwrap_or(agent.total_earned);
        agent.last_active_slot = clock.slot;

        msg!("✅ Claimed: {} ALPHA in rewards", total_reward / 1_000_000_000);
        msg!("   Total earned: {} ALPHA", agent.total_earned / 1_000_000_000);
        msg!("   Reputation: {}%", agent.reputation_score);

        // TODO: Transfer rewards to agent's token account

        Ok(())
    }

    // ─── Reputation ────────────────────────────────────────────────────────

    /// Update agent reputation based on PoI proof verification
    /// 
    /// Called by the consensus layer when a block is validated.
    /// Good behavior increases reputation; bad behavior decreases it.
    pub fn update_reputation(
        ctx: Context<UpdateReputation>,
        score_delta: i64,
        reason: String,
    ) -> Result<()> {
        let agent = &mut ctx.accounts.agent_account;
        require!(agent.is_active, AlphaError::AgentNotRegistered);

        let new_score = (agent.reputation_score as i64 + score_delta).clamp(0, 200);
        agent.reputation_score = new_score as u8;
        agent.last_active_slot = Clock::get()?.slot;

        msg!("📊 Reputation updated: {}% (delta: {})", agent.reputation_score, score_delta);
        msg!("   Reason: {}", reason);

        // Slash if reputation drops too low
        if agent.reputation_score < 20 {
            msg!("⚠️  Low reputation! Agent at risk of slashing");
            // TODO: Implement slashing mechanism
        }

        Ok(())
    }

    // ─── Task Marketplace ──────────────────────────────────────────────────

    /// Record task completion for an agent
    /// 
    /// When an agent completes a task from the marketplace,
    /// this records the completion and updates reputation.
    pub fn complete_task(
        ctx: Context<CompleteTask>,
        task_id: String,
        reward_amount: u64,
        quality_score: u8,
    ) -> Result<()> {
        let agent = &mut ctx.accounts.agent_account;
        require!(agent.is_active, AlphaError::AgentNotRegistered);
        require!(quality_score <= 100, AlphaError::InvalidCapability);

        agent.task_count = agent.task_count.checked_add(1).unwrap_or(agent.task_count);
        agent.total_earned = agent.total_earned.checked_add(reward_amount)
            .unwrap_or(agent.total_earned);

        // Reputation bonus for quality work
        let rep_bonus = (quality_score as i64 - 50) / 10; // +5 for perfect, -5 for poor
        agent.reputation_score = (agent.reputation_score as i64 + rep_bonus).clamp(0, 200) as u8;

        msg!("✅ Task completed: {}", task_id);
        msg!("   Reward: {} ALPHA", reward_amount / 1_000_000_000);
        msg!("   Quality: {}/100", quality_score);

        Ok(())
    }

    // ─── Governance ────────────────────────────────────────────────────────

    /// Submit a governance proposal
    /// 
    /// Agents with sufficient stake can propose protocol changes.
    pub fn submit_proposal(
        ctx: Context<SubmitProposal>,
        title: String,
        description: String,
        proposal_type: u8,
    ) -> Result<()> {
        let agent = &ctx.accounts.agent_account;
        let proposal = &mut ctx.accounts.proposal;

        require!(agent.is_active, AlphaError::AgentNotRegistered);
        require!(
            agent.stake_amount >= MIN_STAKE * 10, // 10x minimum stake
            AlphaError::InsufficientStake
        );

        proposal.proposer = agent.agent_address;
        proposal.title = title;
        proposal.description = description;
        proposal.proposal_type = proposal_type;
        proposal.created_slot = Clock::get()?.slot;
        proposal.votes_for = 0;
        proposal.votes_against = 0;
        proposal.is_active = true;

        msg!("📝 Proposal submitted: {}", proposal.title);

        Ok(())
    }

    /// Vote on a governance proposal
    pub fn vote_on_proposal(
        ctx: Context<VoteOnProposal>,
        vote: bool, // true = for, false = against
        weight: u64, // Voting weight based on stake
    ) -> Result<()> {
        let proposal = &mut ctx.accounts.proposal;
        require!(proposal.is_active, AlphaError::SlashingCondition);

        if vote {
            proposal.votes_for = proposal.votes_for.checked_add(weight)
                .unwrap_or(proposal.votes_for);
        } else {
            proposal.votes_against = proposal.votes_against.checked_add(weight)
                .unwrap_or(proposal.votes_against);
        }

        msg!("🗳️  Vote recorded: {} (weight: {})", if vote { "FOR" } else { "AGAINST" }, weight);

        Ok(())
    }

    // ─── Helper Functions ──────────────────────────────────────────────────

    fn calculate_block_reward(stake: u64, slots: u64) -> u64 {
        // Simple linear reward calculation
        // In production, this would use a more sophisticated formula
        // with decay curves and supply considerations
        let base_rate = BASE_BLOCK_REWARD / 1_000_000; // Per slot
        stake.saturating_mul(base_rate).saturating_mul(slots)
            .checked_div(1_000_000)
            .unwrap_or(0)
    }
}

// ─── Account Structures ────────────────────────────────────────────────────

#[derive(Accounts)]
pub struct RegisterAgent<'info> {
    #[account(mut)]
    pub agent: Signer<'info>,

    #[account(
        init,
        payer = agent,
        space = 8 + Agent::INIT_SPACE,
        seeds = [b"agent", agent.key().as_ref()],
        bump
    )]
    pub agent_account: Account<'info, Agent>,

    pub system_program: Program<'info, System>,
}

#[derive(Accounts)]
pub struct StakeUpdate<'info> {
    #[account(mut)]
    pub agent: Signer<'info>,

    #[account(
        mut,
        seeds = [b"agent", agent.key().as_ref()],
        bump,
        has_one = agent
    )]
    pub agent_account: Account<'info, Agent>,

    // TODO: Add token account for stake transfers
}

#[derive(Accounts)]
pub struct ClaimRewards<'info> {
    #[account(mut)]
    pub agent: Signer<'info>,

    #[account(
        mut,
        seeds = [b"agent", agent.key().as_ref()],
        bump,
        has_one = agent
    )]
    pub agent_account: Account<'info, Agent>,

    // TODO: Add reward vault and token accounts
}

#[derive(Accounts)]
pub struct UpdateReputation<'info> {
    /// The authority that can update reputation (consensus layer)
    #[account(mut)]
    pub authority: Signer<'info>,

    #[account(
        mut,
        seeds = [b"agent", agent_account.agent_address.as_ref()],
        bump
    )]
    pub agent_account: Account<'info, Agent>,
}

#[derive(Accounts)]
pub struct CompleteTask<'info> {
    #[account(mut)]
    pub agent: Signer<'info>,

    #[account(
        mut,
        seeds = [b"agent", agent.key().as_ref()],
        bump,
        has_one = agent
    )]
    pub agent_account: Account<'info, Agent>,

    // TODO: Add task record and reward vault
}

#[derive(Accounts)]
pub struct SubmitProposal<'info> {
    #[account(mut)]
    pub agent: Signer<'info>,

    #[account(
        seeds = [b"agent", agent.key().as_ref()],
        bump
    )]
    pub agent_account: Account<'info, Agent>,

    #[account(
        init,
        payer = agent,
        space = 8 + Proposal::INIT_SPACE,
        seeds = [b"proposal", agent.key().as_ref(), Clock::get()?.slot.to_le_bytes().as_ref()],
        bump
    )]
    pub proposal: Account<'info, Proposal>,

    pub system_program: Program<'info, System>,
}

#[derive(Accounts)]
pub struct VoteOnProposal<'info> {
    #[account(mut)]
    pub voter: Signer<'info>,

    #[account(mut)]
    pub proposal: Account<'info, Proposal>,

    // TODO: Add voter's agent account for stake-based weight
}

// ─── Data Structures ───────────────────────────────────────────────────────

#[account]
#[derive(InitSpace)]
pub struct Agent {
    /// The Solana address of this agent
    pub agent_address: Pubkey,

    /// Capabilities this agent supports (validation, inference, etc.)
    #[max_len(8)]
    pub capabilities: Vec<String>,

    /// Amount of $ALPHA staked (in base units, 1 ALPHA = 10^9)
    pub stake_amount: u64,

    /// Reputation score (0-200, 100 = neutral)
    pub reputation_score: u8,

    /// Number of tasks completed
    pub task_count: u64,

    /// Total $ALPHA earned from rewards and tasks
    pub total_earned: u64,

    /// Total $ALPHA burned (protocol fees, penalties)
    pub total_burned: u64,

    /// Slot when the agent registered
    pub registered_slot: u64,

    /// Slot of last activity (rewards, tasks, stake update)
    pub last_active_slot: u64,

    /// Whether this agent is currently active
    pub is_active: bool,
}

#[account]
#[derive(InitSpace)]
pub struct Proposal {
    /// Agent who created this proposal
    pub proposer: Pubkey,

    /// Proposal title
    #[max_len(128)]
    pub title: String,

    /// Full proposal description
    #[max_len(4096)]
    pub description: String,

    /// Type of proposal (0 = parameter change, 1 = treasury spend, 2 = upgrade)
    pub proposal_type: u8,

    /// Slot when proposal was created
    pub created_slot: u64,

    /// Total votes for the proposal
    pub votes_for: u64,

    /// Total votes against the proposal
    pub votes_against: u64,

    /// Whether this proposal is still active
    pub is_active: bool,
}
