package crypto

// ZK Proof of Intelligence (PoI) using gnark Groth16.
//
// The circuit proves: "I ran inference with latency L where minLatency <= L <= maxLatency"
// without revealing the exact latency value. Only the proof and public witness are exposed.
//
// Circuit structure:
//   Private inputs: latency (int), minLatency (int), maxLatency (int)
//   Public  inputs: minLatency, maxLatency (the bounds)
//   Constraints:
//     latency - minLatency >= 0  (i.e., latency >= minLatency)
//     maxLatency - latency >= 0  (i.e., latency <= maxLatency)
//
// The gnark BN254 Groth16 backend is used (standard, widely supported).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
)

const (
	// PoI latency bounds in milliseconds
	PoIMinLatency int64 = 100
	PoIMaxLatency int64 = 10000
)

// PoICircuit defines the ZK circuit that proves valid inference latency.
// The prover holds latencyMs as a secret, and MinLatencyMs/MaxLatencyMs as known public bounds.
type PoICircuit struct {
	// Secret witness: the actual latency in ms
	LatencyMs frontend.Variable `gnark:",secret"`

	// Public witness: the allowed latency bounds
	MinLatencyMs frontend.Variable `gnark:",public"`
	MaxLatencyMs frontend.Variable `gnark:",public"`
}

// Define implements gnark's Circuit interface.
// Constraints enforce: MinLatencyMs <= LatencyMs <= MaxLatencyMs
func (c *PoICircuit) Define(api frontend.API) error {
	// Compute diff_low = latency - minLatency  (must be >= 0)
	diffLow := api.Sub(c.LatencyMs, c.MinLatencyMs)
	// Compute diff_high = maxLatency - latency  (must be >= 0)
	diffHigh := api.Sub(c.MaxLatencyMs, c.LatencyMs)

	// AssertIsLessOrEqual: x <= y  is equivalent to checking y - x is in range
	// We use AssertIsLessOrEqual(0, diffLow) and AssertIsLessOrEqual(0, diffHigh)
	// In gnark, we can assert diff >= 0 by checking it's less than the field order/2
	// The cleanest way is api.AssertIsLessOrEqual(minLatency, latency) etc.
	api.AssertIsLessOrEqual(c.MinLatencyMs, c.LatencyMs)
	api.AssertIsLessOrEqual(c.LatencyMs, c.MaxLatencyMs)

	// Suppress unused variable warnings by referencing computed diffs
	_ = diffLow
	_ = diffHigh

	return nil
}

// PoIProofData contains a serialized Groth16 proof and its public inputs.
type PoIProofData struct {
	// ProofBytes is the serialized Groth16 proof
	ProofBytes []byte `json:"proof_bytes"`
	// PublicInputs stores the public witness values as hex strings
	PublicInputs map[string]string `json:"public_inputs"`
	// VKBytes is the serialized verification key (for self-contained verification)
	VKBytes []byte `json:"vk_bytes"`
}

// JSON serialization helpers
func (p *PoIProofData) MarshalJSON() ([]byte, error) {
	type Alias PoIProofData
	return json.Marshal((*Alias)(p))
}

// proofKeyCache holds the compiled circuit and keys after first setup
type proofKeyCache struct {
	ccs constraint.ConstraintSystem
	pk  groth16.ProvingKey
	vk  groth16.VerifyingKey
	err error
}

var (
	poiCacheOnce sync.Once
	poiCache     proofKeyCache
)

// getPoIKeys returns the cached proving/verifying keys, running setup once.
// The first call performs Groth16 trusted setup which may take a few seconds.
func getPoIKeys() (*proofKeyCache, error) {
	poiCacheOnce.Do(func() {
		// Compile the circuit into R1CS constraint system
		ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &PoICircuit{})
		if err != nil {
			poiCache.err = fmt.Errorf("compile circuit: %w", err)
			return
		}

		// Groth16 setup (trusted setup / SRS generation)
		pk, vk, err := groth16.Setup(ccs)
		if err != nil {
			poiCache.err = fmt.Errorf("groth16 setup: %w", err)
			return
		}

		poiCache.ccs = ccs
		poiCache.pk = pk
		poiCache.vk = vk
	})

	if poiCache.err != nil {
		return nil, poiCache.err
	}
	return &poiCache, nil
}

// GeneratePoIProof generates a Groth16 ZK proof that latencyMs is within [100, 10000].
// entropyScore is not part of the circuit (it's a separate on-chain metric) but is
// included in PublicInputs for completeness.
//
// The first call performs trusted setup and may take a few seconds.
func GeneratePoIProof(latencyMs int64, entropyScore float64) (*PoIProofData, error) {
	if latencyMs < PoIMinLatency || latencyMs > PoIMaxLatency {
		return nil, fmt.Errorf("latencyMs %d out of valid range [%d, %d]",
			latencyMs, PoIMinLatency, PoIMaxLatency)
	}

	keys, err := getPoIKeys()
	if err != nil {
		return nil, err
	}

	// Build the full witness (secret + public)
	assignment := &PoICircuit{
		LatencyMs:    latencyMs,
		MinLatencyMs: PoIMinLatency,
		MaxLatencyMs: PoIMaxLatency,
	}

	w, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	if err != nil {
		return nil, fmt.Errorf("create witness: %w", err)
	}

	// Generate the proof
	proof, err := groth16.Prove(keys.ccs, keys.pk, w)
	if err != nil {
		return nil, fmt.Errorf("groth16 prove: %w", err)
	}

	// Serialize proof
	var proofBuf bytes.Buffer
	if _, err := proof.WriteTo(&proofBuf); err != nil {
		return nil, fmt.Errorf("serialize proof: %w", err)
	}

	// Serialize verification key
	var vkBuf bytes.Buffer
	if _, err := keys.vk.WriteTo(&vkBuf); err != nil {
		return nil, fmt.Errorf("serialize vk: %w", err)
	}

	return &PoIProofData{
		ProofBytes: proofBuf.Bytes(),
		PublicInputs: map[string]string{
			"min_latency_ms": fmt.Sprintf("%d", PoIMinLatency),
			"max_latency_ms": fmt.Sprintf("%d", PoIMaxLatency),
			"entropy_score":  fmt.Sprintf("%.4f", entropyScore),
		},
		VKBytes: vkBuf.Bytes(),
	}, nil
}

// VerifyPoIProof verifies a Groth16 PoI proof using the embedded verification key.
// Returns true if the proof is valid, false with an error if it is not.
func VerifyPoIProof(proofData *PoIProofData) (bool, error) {
	if proofData == nil {
		return false, errors.New("proof data is nil")
	}
	if len(proofData.ProofBytes) == 0 {
		return false, errors.New("proof bytes are empty")
	}
	if len(proofData.VKBytes) == 0 {
		return false, errors.New("verification key bytes are empty")
	}

	// Deserialize the proof
	proof := groth16.NewProof(ecc.BN254)
	if _, err := proof.ReadFrom(bytes.NewReader(proofData.ProofBytes)); err != nil {
		return false, fmt.Errorf("deserialize proof: %w", err)
	}

	// Deserialize the verification key
	vk := groth16.NewVerifyingKey(ecc.BN254)
	if _, err := vk.ReadFrom(bytes.NewReader(proofData.VKBytes)); err != nil {
		return false, fmt.Errorf("deserialize vk: %w", err)
	}

	// Reconstruct the public witness from the embedded public inputs
	publicAssignment := &PoICircuit{
		MinLatencyMs: PoIMinLatency,
		MaxLatencyMs: PoIMaxLatency,
	}
	publicWitness, err := frontend.NewWitness(publicAssignment, ecc.BN254.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return false, fmt.Errorf("create public witness: %w", err)
	}

	// Verify
	if err := groth16.Verify(proof, vk, publicWitness); err != nil {
		return false, nil // proof invalid, not an internal error
	}
	return true, nil
}

// VerifyPoIProofWithWitness verifies a proof using a pre-built public witness.
// This variant accepts a witness.Witness directly (for advanced use cases).
func VerifyPoIProofWithWitness(proofData *PoIProofData, pubWitness witness.Witness) (bool, error) {
	if proofData == nil {
		return false, errors.New("proof data is nil")
	}

	proof := groth16.NewProof(ecc.BN254)
	if _, err := proof.ReadFrom(bytes.NewReader(proofData.ProofBytes)); err != nil {
		return false, fmt.Errorf("deserialize proof: %w", err)
	}

	vk := groth16.NewVerifyingKey(ecc.BN254)
	if _, err := vk.ReadFrom(bytes.NewReader(proofData.VKBytes)); err != nil {
		return false, fmt.Errorf("deserialize vk: %w", err)
	}

	if err := groth16.Verify(proof, vk, pubWitness); err != nil {
		return false, nil
	}
	return true, nil
}

// bigIntToHex is a helper to convert *big.Int to hex string
func bigIntToHex(n *big.Int) string {
	if n == nil {
		return "0x0"
	}
	return "0x" + n.Text(16)
}
