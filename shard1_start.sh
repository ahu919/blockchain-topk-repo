#!/bin/bash
# 用法：./shard1_start.sh <mode>
# mode: tpcb_full | tpcb_abl1 | zipf_full | zipf_abl1

MODE=${1:-tpcb_full}

# --- 配置参数（可自行修改） ---
SHARD_DIR="${SHARD_DIR:-$HOME/shard1-clique}"
GETH_IPC="${SHARD_DIR}/geth.ipc"
HTTP_PORT="${HTTP_PORT:-8547}"
SHARD_APP_PORT="${SHARD_APP_PORT:-8081}"
# 请替换为你自己的测试账户地址
ACCOUNT="${ACCOUNT:-0xYourTestAccountAddressHere}" 
PASSWORD_FILE="${SHARD_DIR}/password.txt"
SHARD_BUILD_DIR="${SHARD_BUILD_DIR:-$HOME/shard_build}"
SHARD_BUILD_SCRIPT="${SHARD_BUILD_DIR}/shard2_new.go"
ZIPF_CONTRACT="${ZIPF_CONTRACT:-0xYourZipfContractAddressHere}"
SERVICE_LOG="${SHARD_DIR}/shard1_service.log"

# 根据 mode 设置环境变量
case $MODE in
  tpcb_full) unset CONTRACT_ADDR; unset ABLATION_FIXED_QUANTILE ;;
  tpcb_abl1) unset CONTRACT_ADDR; export ABLATION_FIXED_QUANTILE=1 ;;
  zipf_full) export CONTRACT_ADDR=$ZIPF_CONTRACT; unset ABLATION_FIXED_QUANTILE ;;
  zipf_abl1) export CONTRACT_ADDR=$ZIPF_CONTRACT; export ABLATION_FIXED_QUANTILE=1 ;;
  *) echo "用法：$0 {tpcb_full|tpcb_abl1|zipf_full|zipf_abl1}"; exit 1 ;;
esac

echo "模式：$MODE"

# 1. 检查 geth，没运行就启动
if pgrep -f "geth --networkid 4321" > /dev/null; then
  echo "Geth 已在运行，跳过启动"
else
  echo "启动 Geth 节点..."
  nohup geth --networkid 4321 \
    --datadir "$SHARD_DIR" \
    --unlock "$ACCOUNT" \
    --password "$PASSWORD_FILE" \
    --mine --miner.etherbase "$ACCOUNT" \
    --miner.gasprice 0 \
    --http --http.addr "0.0.0.0" --http.port "$HTTP_PORT" \
    --http.api eth,net,web3,personal \
    --ipcpath "$GETH_IPC" \
    --allow-insecure-unlock --nodiscover --syncmode full \
    --txpool.accountslots 1024 --txpool.globalslots 8192 \
    --gcmode archive \
    --verbosity 2 \
    > "$SHARD_DIR/geth.log" 2>&1 &
  
  echo "等待 geth.IPC 就绪..."
  for i in {1..30}; do
    if [ -S "$GETH_IPC" ]; then
      echo "Geth IPC 已就绪"; break
    fi
    sleep 1
  done
fi

# 2. 停止旧的 Go 服务
pkill -f shard1_server 2>/dev/null
sleep 2

# 3. 启动 Go 服务
echo "启动 Go 服务"
BINARY="${SHARD_BUILD_DIR}/shard1_server"
if [ ! -f "${BINARY}" ] || [ "${SHARD_BUILD_SCRIPT}" -nt "${BINARY}" ]; then
  echo "重新编译分片服务..."
  cd "${SHARD_BUILD_DIR}" || exit
  go build -o "${BINARY}" "${SHARD_BUILD_SCRIPT}"
fi

export PORT=${SHARD_APP_PORT}
nohup "${BINARY}" > "$SERVICE_LOG" 2>&1 &
echo "分片1Go服务已启动，PID：$!"

# 4. 等Go服务加载完成
sleep 30

if grep -q "分片服务启动" "$SERVICE_LOG"; then
  echo "启动成功"
  grep "合约中共有\|分片服务启动" "$SERVICE_LOG" | tail - 2
else
  echo "启动失败"
  tail - 10 "$SERVICE_LOG"
fi
