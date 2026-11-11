Verifiable Cross-Shard Top-K Query in Blockchain Databases
This repository provides the official open-source code and experimental artifacts for the paper "Verifiable Top-K Queries in Cross-Shard Blockchain Databases". The project implements a sharded blockchain environment based on the Geth client, featuring the Query-Aware Adaptive Threshold Search (RAQP) algorithm and the Verifiable Merkle Interval Tree (VMIT).

1. Directory Structure
chain/: Contains genesis.json (Clique PoA genesis configuration, ChainID: 4321).

contracts/: Contains DataStore.sol (smart contract source code), DataStoreFixed.abi, and DataStoreFixed.bin.

coordinator/: Coordinator node source code (coordinator_http.go), implementing the RAQP algorithm, baseline binary search, and proof verification.

shard/: Shard node source code (shard2_new.go), responsible for building the VMIT and generating existence/integrity proofs.

datasets/: Dataset processing scripts.

split_16_hash.py: TPC-H data splitting script based on hash.

generate_zipf.py: Zipf skewed data generation script (α=1.2, truncated at 95,000).

deploy_and_insert.py: Automated contract deployment and data insertion script.

scripts/: Contains shard1_start.sh for one-click node startup.

results/: Raw experimental logs and result summaries.

go.mod / go.sum: Go language dependency files.

2. Requirements
OS: Ubuntu 22.04 LTS.

Geth Node: v1.10.26-stable (Note: Geth v1.13+ removed the personal API, so this specific version is required).

Go: Go 1.19 or higher.

Python: Python 3.8+ (with web3 and numpy installed).

Network Simulation: Linux tc tool used for bandwidth and delay limitation in experiments.

3. Quick Start
3.1 Dataset Preparation
Install Python dependencies: pip install web3 numpy

Generate Zipf skewed data (creates 16 CSV files automatically): python3 datasets/generate_zipf.py

Or split the raw TPC-H data: python3 datasets/split_16_hash.py

3.2 Compilation
Compile the coordinator: go build -o coordinator coordinator/coordinator_http.go

Compile the shard service: go build -o shard shard/shard2_new.go

3.3 Network Setup and Data Insertion
Initialize the private chain using chain/genesis.json and start the Geth nodes.

Start the shard services using scripts/shard1_start.sh. The shard binary is a universal program; to start multiple shards, simply modify the environment variables:

bash
export CONTRACT_ADDR=0xYourContractAddress
export RPC_URL=/path/to/geth.ipc
export PORT=:8081
./shard
Deploy the contract and insert data into a specific shard:
python3 datasets/deploy_and_insert.py <PORT> <CSV_PATH> <ABI_PATH> <BIN_PATH>

3.4 Query Execution
Ensure all shard services are running, then start the coordinator to initiate Top-K queries (K=10~1000):
./coordinator -mode=default

(Alternatively, you can run it directly without compilation: go run coordinator/coordinator_http.go -mode=default)

4. Experimental Configurations
The system includes built-in modes for ablation studies and security tests. These are configured via environment variables:

Ablation 1 (Fixed Quantile Sampling): Set export ABLATION_FIXED_QUANTILE=1 before starting the shard services.

Ablation 2 (Skip Narrow-Interval Estimation): Set export ABLATION_NO_NARROW_INTERVAL=1 before starting the coordinator.

Ablation 3 (Single-Boundary Search): Set export ABLATION_NO_DUAL_BOUNDARY=1 before starting the coordinator.

Malicious Node Test (Forged Quantiles): Set export MALICIOUS=1 before starting a specific shard service. The shard will tamper with the returned data, and the coordinator will identify and exclude it during the verification phase.
