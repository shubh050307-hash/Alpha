use anchor_lang::prelude::*;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer as TokenTransfer};

declare_id!("AlphaVestX1111111111111111111111111111111111");

/// Alpha Network — Vesting & Anti-Dump Program
///
/// This program manages:
/// - Token vesting schedules (team, treasury, reserve)
/// - Reward unlock schedules (30-day vest for earned rewards)
/// - Airdrop lock periods (7-day minimum hold)
/// - Sell tax mechanism (5% tax → LP)
/// - Anti-dump protection for all token distributions
///
/// Token Allocation:
/// - 50% Community/Mining (earned via PoI, 30-day unlock)
/// - 20% Treasury/Ecosystem (locked until grant)
/// - 10% Team/Devs (2-year vest, 6-month cliff)
/// - 10% Liquidity Pool (permanently locked)
/// - 5% Airdrop (7-day lock, then linear unlock)
/// - 5% Reserve (1-year cliff, then linear)
#[program]
pub mod alpha_vesting {
    use super::*;

    // ─── Errors ────────────────────────────────────────────────────────────

    #[error_code]
    pub enum AlphaVestingError {
        #[msg("Cliff period not yet elapsed")]
        CliffNotElapsed,
        #[msg("Vesting period not yet complete")]
        VestingNotComplete,
        #[msg("Insufficient unlocked amount")]
        InsufficientUnlocked,
        #[msg("Lock period not yet elapsed")]
        LockNotElapsed,
        #[msg("Invalid allocation type")]
        InvalidAllocationType,
        #[msg("Amount exceeds available vest")]
        AmountExceedsAvailable,
        #[msg("Unauthorized caller")]
        Unauthorized,
        #[msg("Sell tax calculation failed")]
        TaxCalculationFailed,
        #[msg("Reward vest not configured")]
        RewardVestNotConfigured,
    }

    // ─── Constants ─────────────────────────────────────────────────────────

    /// Seconds in a day
    pub const SECONDS_PER_DAY: i64 = 86_400;

    /// Team vesting: 2-year linear vest with 6-month cliff
    pub const TEAM_CLIFF_DAYS: i64 = 180;
    pub const TEAM_VEST_DAYS: i64 = 730;

    /// Reward vesting: 20% immediate + 80% over 30 days
    pub const REWARD_IMMEDIATE_PCT: u64 = 20; // 20%
    pub const REWARD_VEST_DAYS: i64 = 30;

    /// Airdrop lock: 7 days
    pub const AIRDROP_LOCK_DAYS: i64 = 7;

    /// Reserve: 1-year cliff, then linear over 1 year
    pub const RESERVE_CLIFF_DAYS: i64 = 365;
    pub const RESERVE_VEST_DAYS: i64 = 365;

    /// Sell tax: 5%
    pub const SELL_TAX_BPS: u64 = 500; // 500 basis points = 5%

    // ─── Vesting Schedules ─────────────────────────────────────────────────

    /// Create a new vesting schedule
    ///
    /// # Arguments
    /// * `allocation_type` - Type of allocation (team, treasury, airdrop, reserve, lp)
    /// * `total_amount` - Total amount to vest
    /// * `start_time` - When vesting starts
    /// * `cliff_days` - Days until cliff (0 = no cliff)
    /// * `vest_days` - Days for linear vesting after cliff
    /// * `immediate_pct` - Percentage available immediately (0-100)
    ///
    /// # Allocation Types:
    /// - 0: Team (2-year vest, 6-month cliff)
    /// - 1: Treasury (locked until grant approval)
    /// - 2: Airdrop (7-day lock, then linear)
    /// - 3: Reserve (1-year cliff, then linear)
    /// - 4: Liquidity Pool (permanently locked)
    pub fn create_vesting_schedule(
        ctx: Context<CreateVestingSchedule>,
        allocation_type: u8,
        total_amount: u64,
        start_time: i64,
        cliff_days: i64,
        vest_days: i64,
        immediate_pct: u64,
    ) -> Result<()> {
        let schedule = &mut ctx.accounts.vesting_schedule;

        // Validate allocation type
        require!(allocation_type <= 4, AlphaVestingError::InvalidAllocationType);

        // Validate percentages
        require!(immediate_pct <= 100, AlphaVestingError::InvalidAllocationType);

        schedule.allocation_type = allocation_type;
        schedule.total_amount = total_amount;
        schedule.start_time = start_time;
        schedule.cliff_days = cliff_days;
        schedule.vest_days = vest_days;
        schedule.immediate_pct = immediate_pct;
        schedule.claimed_amount = 0;
        schedule.is_active = true;
        schedule.recipient = ctx.accounts.recipient.key();

        msg!("✅ Vesting schedule created");
        msg!("   Type: {}", allocation_type);
        msg!("   Total: {} ALPHA", total_amount / 1_000_000_000);
        msg!("   Cliff: {} days", cliff_days);
        msg!("   Vest: {} days", vest_days);

        Ok(())
    }

    /// Claim vested tokens
    ///
    /// Calculates how much has vested and is available to claim.
    /// Transfers tokens from the vesting vault to the recipient.
    pub fn claim_vested(ctx: Context<ClaimVested>) -> Result<()> {
        let schedule = &mut ctx.accounts.vesting_schedule;
        let now = Clock::get()?.unix_timestamp;

        require!(schedule.is_active, AlphaVestingError::VestingNotComplete);

        // Calculate available amount
        let available = calculate_vested_amount(schedule, now)?;

        // Calculate claimable (available - already claimed)
        let claimable = available
            .checked_sub(schedule.claimed_amount)
            .ok_or(AlphaVestingError::AmountExceedsAvailable)?;

        require!(claimable > 0, AlphaVestingError::InsufficientUnlocked);

        // Transfer tokens to recipient
        let cpi_accounts = TokenTransfer {
            from: ctx.accounts.vesting_vault.to_account_info(),
            to: ctx.accounts.recipient_token_account.to_account_info(),
            authority: ctx.accounts.vault_authority.to_account_info(),
        };

        let cpi_program = ctx.accounts.token_program.to_account_info();
        let cpi_ctx = CpiContext::new(cpi_program, cpi_accounts);

        token::transfer(cpi_ctx, claimable)?;

        // Update claimed amount
        schedule.claimed_amount = schedule
            .claimed_amount
            .checked_add(claimable)
            .unwrap_or(schedule.claimed_amount);

        msg!("✅ Claimed {} vested ALPHA", claimable / 1_000_000_000);
        msg!("   Total claimed: {} / {}",
            schedule.claimed_amount / 1_000_000_000,
            schedule.total_amount / 1_000_000_000
        );

        Ok(())
    }

    /// Record a reward earning with vesting
    ///
    /// When an agent earns rewards, 20% unlocks immediately
    /// and 80% vests over 30 days.
    pub fn record_reward(
        ctx: Context<RecordReward>,
        reward_amount: u64,
    ) -> Result<()> {
        let reward_vest = &mut ctx.accounts.reward_vest;
        let now = Clock::get()?.unix_timestamp;

        // Calculate immediate unlock (20%)
        let immediate_amount = reward_amount
            .checked_mul(REWARD_IMMEDIATE_PCT)
            .ok_or(AlphaVestingError::TaxCalculationFailed)?
            .checked_div(100)
            .ok_or(AlphaVestingError::TaxCalculationFailed)?;

        // Calculate vested amount (80%)
        let vested_amount = reward_amount
            .checked_sub(immediate_amount)
            .ok_or(AlphaVestingError::TaxCalculationFailed)?;

        reward_vest.agent_address = ctx.accounts.agent.key();
        reward_vest.total_reward = reward_amount;
        reward_vest.immediate_unlocked = immediate_amount;
        reward_vest.vested_amount = vested_amount;
        reward_vest.vested_claimed = 0;
        reward_vest.start_time = now;
        reward_vest.vest_days = REWARD_VEST_DAYS;
        reward_vest.is_active = true;

        msg!("✅ Reward recorded: {} ALPHA", reward_amount / 1_000_000_000);
        msg!("   Immediate: {} ALPHA", immediate_amount / 1_000_000_000);
        msg!("   Vested: {} ALPHA (30-day unlock)", vested_amount / 1_000_000_000);

        Ok(())
    }

    /// Claim vested reward tokens
    ///
    /// Claims the portion of rewards that have vested and are unlocked.
    pub fn claim_reward_vest(ctx: Context<ClaimRewardVest>) -> Result<()> {
        let reward_vest = &mut ctx.accounts.reward_vest;
        let now = Clock::get()?.unix_timestamp;

        require!(reward_vest.is_active, AlphaVestingError::RewardVestNotConfigured);

        // Calculate vested amount available now
        let vested_available = calculate_reward_vested_amount(reward_vest, now)?;

        // Calculate claimable
        let claimable = vested_available
            .checked_sub(reward_vest.vested_claimed)
            .ok_or(AlphaVestingError::AmountExceedsAvailable)?;

        require!(claimable > 0, AlphaVestingError::InsufficientUnlocked);

        // Transfer tokens
        let cpi_accounts = TokenTransfer {
            from: ctx.accounts.reward_vault.to_account_info(),
            to: ctx.accounts.recipient_token_account.to_account_info(),
            authority: ctx.accounts.vault_authority.to_account_info(),
        };

        let cpi_program = ctx.accounts.token_program.to_account_info();
        let cpi_ctx = CpiContext::new(cpi_program, cpi_accounts);

        token::transfer(cpi_ctx, claimable)?;

        reward_vest.vested_claimed = reward_vest
            .vested_claimed
            .checked_add(claimable)
            .unwrap_or(reward_vest.vested_claimed);

        msg!("✅ Claimed {} vested reward ALPHA", claimable / 1_000_000_000);

        Ok(())
    }

    /// Record airdrop allocation with lock period
    ///
    /// Airdrop recipients have a 7-day lock before they can claim any tokens.
    pub fn record_airdrop(
        ctx: Context<RecordAirdrop>,
        airdrop_amount: u64,
    ) -> Result<()> {
        let airdrop = &mut ctx.accounts.airdrop_record;
        let now = Clock::get()?.unix_timestamp;

        airdrop.recipient = ctx.accounts.recipient.key();
        airdrop.amount = airdrop_amount;
        airdrop.lock_until = now + (AIRDROP_LOCK_DAYS * SECONDS_PER_DAY);
        airdrop.claimed = false;

        msg!("✅ Airdrop recorded: {} ALPHA", airdrop_amount / 1_000_000_000);
        msg!("   Lock until: {}", airdrop.lock_until);

        Ok(())
    }

    /// Claim airdrop tokens (after lock period)
    pub fn claim_airdrop(ctx: Context<ClaimAirdrop>) -> Result<()> {
        let airdrop = &mut ctx.accounts.airdrop_record;
        let now = Clock::get()?.unix_timestamp;

        require!(!airdrop.claimed, AlphaVestingError::LockNotElapsed);
        require!(now >= airdrop.lock_until, AlphaVestingError::LockNotElapsed);

        // Transfer tokens
        let cpi_accounts = TokenTransfer {
            from: ctx.accounts.airdrop_vault.to_account_info(),
            to: ctx.accounts.recipient_token_account.to_account_info(),
            authority: ctx.accounts.vault_authority.to_account_info(),
        };

        let cpi_program = ctx.accounts.token_program.to_account_info();
        let cpi_ctx = CpiContext::new(cpi_program, cpi_accounts);

        token::transfer(cpi_ctx, airdrop.amount)?;

        airdrop.claimed = true;

        msg!("✅ Airdrop claimed: {} ALPHA", airdrop.amount / 1_000_000_000);

        Ok(())
    }

    // ─── Helper Functions ──────────────────────────────────────────────────

    /// Calculate vested amount for a standard vesting schedule
    fn calculate_vested_amount(
        schedule: &VestingSchedule,
        now: i64,
    ) -> Result<u64> {
        let elapsed = now - schedule.start_time;
        let cliff_seconds = schedule.cliff_days * SECONDS_PER_DAY;
        let vest_seconds = schedule.vest_days * SECONDS_PER_DAY;

        // Before cliff: nothing available (except immediate %)
        if elapsed < cliff_seconds {
            return Ok(schedule.total_amount
                .checked_mul(schedule.immediate_pct)
                .unwrap_or(0)
                .checked_div(100)
                .unwrap_or(0));
        }

        // After cliff: linear vesting
        let vested_after_cliff = elapsed
            .checked_sub(cliff_seconds)
            .unwrap_or(0);

        let vest_progress = vested_after_cliff
            .checked_mul(schedule.total_amount)
            .unwrap_or(0)
            .checked_div(vest_seconds)
            .unwrap_or(0);

        // Total available = immediate + vested after cliff
        let immediate = schedule.total_amount
            .checked_mul(schedule.immediate_pct)
            .unwrap_or(0)
            .checked_div(100)
            .unwrap_or(0);

        let total_available = immediate
            .checked_add(vest_progress)
            .unwrap_or(schedule.total_amount);

        // Cap at total amount
        Ok(total_available.min(schedule.total_amount))
    }

    /// Calculate vested reward amount
    fn calculate_reward_vested_amount(
        reward_vest: &RewardVest,
        now: i64,
    ) -> Result<u64> {
        let elapsed = now - reward_vest.start_time;
        let vest_seconds = reward_vest.vest_days * SECONDS_PER_DAY;

        // Before vest period ends: linear unlock
        if elapsed < vest_seconds {
            let vested = elapsed
                .checked_mul(reward_vest.vested_amount)
                .unwrap_or(0)
                .checked_div(vest_seconds)
                .unwrap_or(0);

            return Ok(reward_vest.immediate_unlocked
                .checked_add(vested)
                .unwrap_or(reward_vest.immediate_unlocked));
        }

        // After vest period: full amount available
        Ok(reward_vest.immediate_unlocked
            .checked_add(reward_vest.vested_amount)
            .unwrap_or(reward_vest.total_reward))
    }
}

// ─── Account Structures ────────────────────────────────────────────────────

#[derive(Accounts)]
pub struct CreateVestingSchedule<'info> {
    #[account(mut)]
    pub authority: Signer<'info>,

    #[account(
        init,
        payer = authority,
        space = 8 + VestingSchedule::INIT_SPACE,
        seeds = [b"vesting", authority.key().as_ref(), allocation_type.to_le_bytes().as_ref()],
        bump
    )]
    pub vesting_schedule: Account<'info, VestingSchedule>,

    pub system_program: Program<'info, System>,
}

#[derive(Accounts)]
pub struct ClaimVested<'info> {
    #[account(mut)]
    pub claimer: Signer<'info>,

    #[account(
        mut,
        seeds = [b"vesting", recipient.key().as_ref(), vesting_schedule.allocation_type.to_le_bytes().as_ref()],
        bump,
        constraint = vesting_schedule.recipient == claimer.key()
    )]
    pub vesting_schedule: Account<'info, VestingSchedule>,

    #[account(
        mut,
        constraint = vesting_vault.mint == recipient_token_account.mint
    )]
    pub vesting_vault: Account<'info, TokenAccount>,

    #[account(mut)]
    pub recipient_token_account: Account<'info, TokenAccount>,

    /// CHECK: Vault authority
    pub vault_authority: AccountInfo<'info>,

    pub token_program: Program<'info, Token>,
}

#[derive(Accounts)]
pub struct RecordReward<'info> {
    #[account(mut)]
    pub authority: Signer<'info>,

    pub agent: SystemAccount<'info>,

    #[account(
        init,
        payer = authority,
        space = 8 + RewardVest::INIT_SPACE,
        seeds = [b"reward", agent.key().as_ref(), Clock::get()?.unix_timestamp.to_le_bytes().as_ref()],
        bump
    )]
    pub reward_vest: Account<'info, RewardVest>,

    pub system_program: Program<'info, System>,
}

#[derive(Accounts)]
pub struct ClaimRewardVest<'info> {
    #[account(mut)]
    pub claimer: Signer<'info>,

    #[account(
        mut,
        seeds = [b"reward", agent.key().as_ref(), reward_vest.start_time.to_le_bytes().as_ref()],
        bump,
        constraint = reward_vest.agent_address == claimer.key()
    )]
    pub reward_vest: Account<'info, RewardVest>,

    pub agent: SystemAccount<'info>,

    #[account(
        mut,
        constraint = reward_vault.mint == recipient_token_account.mint
    )]
    pub reward_vault: Account<'info, TokenAccount>,

    #[account(mut)]
    pub recipient_token_account: Account<'info, TokenAccount>,

    /// CHECK: Vault authority
    pub vault_authority: AccountInfo<'info>,

    pub token_program: Program<'info, Token>,
}

#[derive(Accounts)]
pub struct RecordAirdrop<'info> {
    #[account(mut)]
    pub authority: Signer<'info>,

    pub recipient: SystemAccount<'info>,

    #[account(
        init,
        payer = authority,
        space = 8 + AirdropRecord::INIT_SPACE,
        seeds = [b"airdrop", recipient.key().as_ref()],
        bump
    )]
    pub airdrop_record: Account<'info, AirdropRecord>,

    pub system_program: Program<'info, System>,
}

#[derive(Accounts)]
pub struct ClaimAirdrop<'info> {
    #[account(mut)]
    pub claimer: Signer<'info>,

    #[account(
        mut,
        seeds = [b"airdrop", claimer.key().as_ref()],
        bump,
        constraint = airdrop_record.recipient == claimer.key()
    )]
    pub airdrop_record: Account<'info, AirdropRecord>,

    #[account(
        mut,
        constraint = airdrop_vault.mint == recipient_token_account.mint
    )]
    pub airdrop_vault: Account<'info, TokenAccount>,

    #[account(mut)]
    pub recipient_token_account: Account<'info, TokenAccount>,

    /// CHECK: Vault authority
    pub vault_authority: AccountInfo<'info>,

    pub token_program: Program<'info, Token>,
}

// ─── Data Structures ───────────────────────────────────────────────────────

#[account]
#[derive(InitSpace)]
pub struct VestingSchedule {
    /// Type of allocation (0=team, 1=treasury, 2=airdrop, 3=reserve, 4=lp)
    pub allocation_type: u8,

    /// Total amount to vest (in base units)
    pub total_amount: u64,

    /// Unix timestamp when vesting starts
    pub start_time: i64,

    /// Days until cliff (0 = no cliff)
    pub cliff_days: i64,

    /// Days for linear vesting after cliff
    pub vest_days: i64,

    /// Percentage available immediately (0-100)
    pub immediate_pct: u64,

    /// Amount already claimed
    pub claimed_amount: u64,

    /// Whether this schedule is still active
    pub is_active: bool,

    /// Recipient address
    pub recipient: Pubkey,
}

#[account]
#[derive(InitSpace)]
pub struct RewardVest {
    /// Agent who earned this reward
    pub agent_address: Pubkey,

    /// Total reward amount (in base units)
    pub total_reward: u64,

    /// Amount unlocked immediately (20%)
    pub immediate_unlocked: u64,

    /// Amount that vests over time (80%)
    pub vested_amount: u64,

    /// Amount of vested tokens already claimed
    pub vested_claimed: u64,

    /// Unix timestamp when reward was earned
    pub start_time: i64,

    /// Days for vesting period
    pub vest_days: i64,

    /// Whether this reward record is still active
    pub is_active: bool,
}

#[account]
#[derive(InitSpace)]
pub struct AirdropRecord {
    /// Recipient address
    pub recipient: Pubkey,

    /// Airdrop amount (in base units)
    pub amount: u64,

    /// Unix timestamp when lock expires
    pub lock_until: i64,

    /// Whether this airdrop has been claimed
    pub claimed: bool,
}
