import csv
import time
import json
from web3 import Web3

# ================= 配置区域 =================
# 连接到本地 Geth 节点 (可通过启动脚本修改为 8547 等)
RPC_URL = 'http://127.0.0.1:8547'
# 你的测试账户地址 (Geth 需解锁该账户)
ACCOUNT = Web3.to_checksum_address("0xYourTestAccountAddressHere")
# 部署好的合约地址
CONTRACT_ADDRESS = Web3.to_checksum_address("0xYourContractAddressHere")
# 数据文件路径
CSV_PATH = "./data/shard_1.csv"
# ABI 文件路径
ABI_PATH = "./DataStoreFixed.abi"
BATCH_SIZE = 100

# 连接节点
w3 = Web3(Web3.HTTPProvider(RPC_URL))
assert w3.is_connected(), "连接 Geth 失败，请检查节点是否启动"

with open(ABI_PATH) as f:
    abi = json.load(f)
contract = w3.eth.contract(address=CONTRACT_ADDRESS, abi=abi)

# 读取 CSV 数据
ids, values = [], []
with open(CSV_PATH, 'r') as f:
    reader = csv.reader(f)
    next(reader)  # 跳过表头
    for row in reader:
        if len(row) >= 2:
            ids.append(int(row[0].strip()))
            values.append(int(row[1].strip()))

print(f"准备插入 {len(ids)} 条记录")

# 分批插入
for i in range(0, len(ids), BATCH_SIZE):
    batch_ids = ids[i:i+BATCH_SIZE]
    batch_vals = values[i:i+BATCH_SIZE]
    batch_num = i // BATCH_SIZE + 1
    print(f"正在插入第 {batch_num} 批...")

    nonce = w3.eth.get_transaction_count(ACCOUNT, 'pending')
    tx_data = contract.encode_abi('batchInsert', args=(batch_ids, batch_vals))
    
    # 构建交易 (Geth 节点需处于 --allow-insecure-unlock 模式)
    tx = {
        'from': ACCOUNT,
        'to': CONTRACT_ADDRESS,
        'data': tx_data,
        'gas': hex(8000000),
        'gasPrice': hex(2000000000),
        'nonce': hex(nonce)
    }

    try:
        tx_hash = w3.provider.make_request('eth_sendTransaction', [tx])['result']
        # 简单等待接收凭证
        receipt = w3.eth.wait_for_transaction_receipt(tx_hash, timeout=30)
        if receipt.status == 1:
            print(f"  第 {batch_num} 批成功")
        else:
            print(f"  第 {batch_num} 批失败 (status=0)")
    except Exception as e:
        print(f"  第 {batch_num} 批出错: {e}")

    time.sleep(0.5) # 防止节点拥堵

# 最终验证
total = contract.functions.getCount().call()
print(f"\n最终合约记录数: {total}")
