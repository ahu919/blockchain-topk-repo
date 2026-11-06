RAQP-VMIT: Verifiable Cross-Shard Top-K Query in Blockchain Databases
本仓库为论文《可验证跨分片 Top-K 查询机制 (RAQP-VMIT)》的官方开源代码与实验复现工程。本工程基于 Geth 客户端构建了多分片区块链环境，实现了高效的自适应阈值搜索算法（RAQP）与可验证默克尔区间树（VMIT）。

📂 1. 目录结构
text
.
├── chain/
│   └── genesis.json            # Clique PoA 创世区块配置 (ChainID: 4321)
├── contracts/
│   ├── DataStore.sol           # 智能合约 Solidity 源码
│   ├── DataStoreFixed.abi      # 合约 ABI (接口描述)
│   └── DataStoreFixed.bin      # 合约 Bytecode (字节码)
├── coordinator/
│   └── coordinator_http.go     # 协调者节点源码 (包含 RAQP 算法、二分法基线、证明验证)
├── shard/
│   └── shard2_new.go           # 分片节点源码 (构建 VMIT、生成存在性/完整性证明、HTTP 接口)
├── datasets/
│   ├── split_16_hash.py        # TPC-H 数据切分脚本 (按 Hash 均匀切分为 16 片)
│   ├── generate_zipf.py        # Zipf 倾斜数据生成脚本 (α=1.2, 截断至 95000)
│   └── deploy_and_insert.py    # 自动化合约部署与数据批量上链脚本
├── scripts/
│   └── shard1_start.sh         # 分片节点一键启动脚本 (包含 Geth 启动与服务启动)
├── results/
│   └── experiment_summary.md   # 核心实验结果汇总 (包含耗时、通信量、验证开销分解)
├── go.mod / go.sum             # Go 语言依赖清单
└── README.md                   # 项目说明文档
🛠️ 2. 运行环境要求
操作系统: Ubuntu 22.04 LTS (或同级别 Linux 发行版)

Geth 节点: v1.10.26-stable (注意：Geth 1.13+ 移除了 personal API，请务必使用此版本)

Go 语言: Go 1.19 或以上版本 (推荐 Go 1.21+)

Python: Python 3.8+ (依赖 web3.py、numpy)

网络模拟: 实验使用 Linux tc 工具限制带宽和延迟。

🚀 3. 快速复现指南
3.1 生成数据集
bash
# 安装 Python 依赖
pip install web3 numpy

# 生成 Zipf 倾斜数据 (自动生成 16 个 CSV 文件)
python3 datasets/generate_zipf.py

# 或者使用切分脚本处理 TPC-H 原始数据 (需自行提供 lineitem.tbl)
python3 datasets/split_16_hash.py
3.2 编译源码
bash
# 编译协调者
go build -o coordinator_bin coordinator/coordinator_http.go

# 编译分片服务
go build -o shard_server shard/shard2_new.go
3.3 启动分片网络与数据插入
使用 chain/genesis.json 初始化私链并启动 Geth 节点。

使用 scripts/shard1_start.sh 启动 Geth 与分片服务（本仓库的 shard_server 是通用程序，启动 16 个分片只需修改环境变量，无需复制代码）：

bash
export CONTRACT_ADDR=0x你的合约地址
export RPC_URL=/path/to/geth.ipc
export PORT=:8081
./shard_server
使用 datasets/deploy_and_insert.py 向指定分片部署合约并灌入数据：

bash
python3 datasets/deploy_and_insert.py <端口> <CSV路径> <ABI路径> <BIN路径>
3.4 运行查询与验证
确保 16 个分片服务已全部启动后，运行协调者发起 Top-K 查询（K=10~1000）：

bash
./coordinator_bin -mode=default
🧪 4. 消融实验与安全测试说明
为了极简地复现论文中的消融实验与安全测试，本工程通过环境变量控制运行模式，无需修改源码：

消融 1 (固定分位数采样)：启动分片时设置 export ABLATION_FIXED_QUANTILE=1。

消融 2 (跳过窄区间估计)：启动协调者时设置 export ABLATION_NO_NARROW_INTERVAL=1。

消融 3 (单边界搜索)：启动协调者时设置 export ABLATION_NO_DUAL_BOUNDARY=1。

恶意节点测试 (伪造分位数)：启动特定分片时设置 export MALICIOUS=1，该分片将篡改数据，协调者会在验证阶段识别并剔除该节点。

📊 5. 核心实验结果
在 100 Mbps 带宽限制下，RAQP 协议与二分法基线（20轮固定搜索）的对比如下：

K 值	二分法轮数	二分法耗时 (ms)	RAQP 轮数	RAQP 耗时 (ms)	RAQP 状态
10	20	237.26	1	53.19	区间收敛
50	20	319.32	2	88.14	区间收敛
100	20	300.32	1	97.82	区间收敛
200	20	323.07	3	102.45	区间收敛
详细验证开销分解与原始控制台日志请参阅 results/ 目录。

⚠️ 6. 安全与合规声明
隐私脱敏：本仓库内所有代码、配置及脚本均不包含真实的生产环境私钥、密码或 keystore 文件。代码内的 IP 已替换为 127.0.0.1 或占位符。

数据体积限制：为保证仓库轻量化，未上传百兆级原始 CSV 数据集，仅提供数据生成与切分脚本。

运行环境限制：因原实验云服务器（阿里云 ECS）已释放，无法提供在线 16 节点环境。本仓库提供全套核心逻辑源码、配置及脚本，支持在本地局域网或 Docker 环境中完整复现核心算法与证明流程。
