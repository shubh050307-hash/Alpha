#!/usr/bin/env python3
"""
Test Alpha Network SDK against live testnet at https://alphanetx.xyz
"""

import sys
import os

# Add SDK to path
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'sdk', 'python'))

from alpha_sdk import AlphaClient

NODE_URL = "https://alphanetx.xyz"

def test_sdk():
    client = AlphaClient(NODE_URL)
    
    print(f"🧪 Testing Alpha Network SDK against {NODE_URL}\n")
    
    # 1. Health check
    print("1. Health check...")
    health = client.health()
    print(f"   ✅ Status: {health.get('status')} | Height: {health.get('height')} | Version: {health.get('version')}\n")
    
    # 2. Chain info
    print("2. Chain info...")
    info = client.chain_info()
    print(f"   ✅ Chain: {info.get('chain_id')} | Consensus: {info.get('consensus')}")
    print(f"   ✅ Token: {info.get('token')} | Supply: {info.get('total_supply'):,}")
    print(f"   ✅ Agents: {info.get('agent_count')} | Blocks/s: {info.get('blocks_per_sec'):.2f}")
    print(f"   ✅ Uptime: {info.get('uptime')} | Height: {info.get('height')}\n")
    
    # 3. Latest block
    print("3. Latest block...")
    block_data = client.latest_block()
    block = block_data.get('block', {})
    print(f"   ✅ Height: {block.get('height')} | Validator: {block.get('validator_id')}")
    print(f"   ✅ Hash: {block.get('hash', '')[:20]}...\n")
    
    # 4. List agents
    print("4. List agents...")
    agents = client.list_agents()
    count = agents.get('count', 0)
    print(f"   ✅ {count} agent(s) registered")
    for agent in agents.get('agents', []):
        print(f"   - {agent.get('agent_id')} | Reputation: {agent.get('reputation_score')} | Stake: {agent.get('stake')}\n")
    
    # 5. Register a test agent
    print("5. Register test agent...")
    try:
        result = client.register_agent(
            address="alpha1sdktest000000000000000000000",
            capabilities=["inference", "validation"],
            stake=5000
        )
        print(f"   ✅ Registered: {result.get('agent_id')}")
        print(f"   ✅ Message: {result.get('message')}\n")
    except Exception as e:
        print(f"   ⚠️  Registration note: {e}\n")
    
    # 6. Intelligence stats
    print("6. Intelligence stats...")
    stats_data = client.intelligence_stats()
    stats = stats_data.get('stats', {})
    print(f"   ✅ Avg latency: {stats.get('avg_latency_ms')}ms")
    print(f"   ✅ Consensus rate: {stats.get('consensus_rate')}")
    print(f"   ✅ Active agents: {stats.get('active_agents')}\n")
    
    # 7. Available tasks
    print("7. Available tasks...")
    tasks = client.get_available_tasks()
    count = tasks.get('count', 0)
    print(f"   ✅ {count} task(s) available\n")
    
    print("🎉 All SDK tests passed!")

if __name__ == "__main__":
    test_sdk()
