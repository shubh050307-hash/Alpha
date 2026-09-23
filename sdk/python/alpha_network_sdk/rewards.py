# sdk/python/alpha_network_sdk/rewards.py
#
# Adds get_rewards() to the SDK client. Powers `alpha-agent info` reward display
# and will power `alpha-agent withdraw` once real SPL token transfer is live.
#
# INTEGRATION: import and call this from wherever the main AlphaClient / agent.py
# class is defined — either paste the method directly into that class, or
# import RewardsMixin and add it to the class bases.

from typing import Any


class RewardsMixin:
    """
    Mixin providing reward-lookup methods. Expects the host class to have
    a `self._get(path: str) -> dict` method already (standard pattern used
    elsewhere in alpha_sdk.py for GET requests).
    """

    def get_rewards(self, address: str) -> dict[str, Any]:
        """
        Fetch all on-chain rewards earned by an agent address.

        Returns:
            {
                "success": bool,
                "address": str,
                "rewards": [
                    {
                        "challenge_id": str,
                        "amount": float,
                        "reason": str,
                        "rank": int,
                        "timestamp": int
                    },
                    ...
                ],
                "total_earned": float,
                "count": int
            }
        """
        return self._get(f"/api/v1/intelligence/rewards/{address}")

    def get_total_earned(self, address: str) -> float:
        """Convenience method — just the total $ALPHA earned, as a float."""
        data = self.get_rewards(address)
        return data.get("total_earned", 0.0)
