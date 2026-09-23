// api/intelligence/handlers_rewards.go
//
// GET /api/v1/intelligence/rewards/{address}
//
// Returns all on-chain reward records earned by a given agent address.
// Reads TxTypeReward events written by challenge_monitor.go on challenge close.
// This is what powers `alpha-agent info` in the SDK.

package intelligence

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RewardsResponse is the JSON shape returned to callers.
type RewardsResponse struct {
	Success bool         `json:"success"`
	Address string       `json:"address"`
	Rewards []RewardItem `json:"rewards"`
	Total   float64      `json:"total_earned"`
	Count   int          `json:"count"`
}

// RewardItem is a single reward record, flattened for the API response.
type RewardItem struct {
	ChallengeID string  `json:"challenge_id"`
	Amount      float64 `json:"amount"`
	Reason      string  `json:"reason"`
	Rank        int     `json:"rank"`
	Timestamp   int64   `json:"timestamp"`
}

// HandleGetRewards handles GET /api/v1/intelligence/rewards/{address}
//
// Wired in registerIntelligenceRoutes() in chain/api/server.go
func (h *Handler) HandleGetRewards(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"success":false,"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}

	// Path: /api/v1/intelligence/rewards/{address}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/intelligence/rewards/")
	address := strings.Split(path, "/")[0]
	if address == "" {
		http.Error(w, `{"success":false,"error":"address required"}`, http.StatusBadRequest)
		return
	}

	rewards, err := h.storage.GetRewardsByAddress(address)
	if err != nil {
		http.Error(w, `{"success":false,"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	var total float64
	items := make([]RewardItem, 0, len(rewards))
	for _, rw := range rewards {
		items = append(items, RewardItem{
			ChallengeID: rw.ChallengeID,
			Amount:      rw.Amount,
			Reason:      rw.Reason,
			Rank:        rw.Rank,
			Timestamp:   rw.Timestamp,
		})
		total += rw.Amount
	}

	resp := RewardsResponse{
		Success: true,
		Address: address,
		Rewards: items,
		Total:   total,
		Count:   len(items),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
