import csv, time, json, sys
from web3 import Web3

# 使用示例: python3 deploy_and_insert.py <PORT> <CSV_PATH> <ABI_PATH> <BIN_PATH>
if len(sys.argv) < 5:
    print("Usage: python3 deploy_and_insert.py <PORT> <CSV_PATH> <ABI_PATH> <BIN_PATH>")
    sys.exit(1)

PORT = sys.argv[1]
CSV_PATH = sys.argv[2]
ABI_PATH = sys.argv[3]
BIN_PATH = sys.argv[4]

RPC_URL = f'http://localhost:{PORT}'
ACCOUNT = Web3.to_checksum_address("0xYourTestAccountAddressHere") # 你的私链账户

w3 = Web3(Web3.HTTPProvider(RPC_URL))
assert w3.is_connected(), "连接失败"

with open(ABI_PATH) as f:
    abi = json.load(f)
with open(BIN_PATH) as f:
    bytecode = f.read().strip()
if not bytecode.startswith('0x'):
    bytecode = '0x' + bytecode

# 1. 部署新合约
print("正在部署新合约...")
nonce = w3.eth.get_transaction_count(ACCOUNT, 'pending')
tx = {'from': ACCOUNT, 'data': bytecode, 'gas': hex(8000000),
      'gasPrice': hex(2000000000), 'nonce': hex(nonce)}
result = w3.provider.make_request('eth_sendTransaction', [tx])
tx_hash = result['result']

new_contract_address = None
for _ in range(60):
    r = w3.provider.make_request('eth_getTransactionReceipt', [tx_hash])
    if r.get('result') and r['result'].get('blockNumber'):
        new_contract_address = r['result']['contractAddress']
        print(f"新合约地址: {new_contract_address}")
        break
    time.sleep(1)

if not new_contract_address:
    raise Exception("部署超时")

# 2. 读取 CSV 并插入数据
contract = w3.eth.contract(address=new_contract_address, abi=abi)
ids, values = [], []
with open(CSV_PATH) as f:
    reader = csv.reader(f)
    next(reader)
    for row in reader:
        if len(row) >= 2:
            ids.append(int(row[0].strip()))
            values.append(int(row[1].strip()))

print(f"准备插入 {len(ids)} 条记录...")
BATCH_SIZE = 100
for i in range(0, len(ids), BATCH_SIZE):
    batch_ids = ids[i:i+BATCH_SIZE]
    batch_vals = values[i:i+BATCH_SIZE]
    nonce = w3.eth.get_transaction_count(ACCOUNT, 'pending')
    tx_data = contract.encode_abi('batchInsert', args=(batch_ids, batch_vals))
    tx = {'from': ACCOUNT, 'to': new_contract_address, 'data': tx_data,
          'gas': hex(8000000), 'gasPrice': hex(2000000000), 'nonce': hex(nonce)}
    res = w3.provider.make_request('eth_sendTransaction', [tx])
    print(f"插入第 {i//BATCH_SIZE + 1} 批...")
    time.sleep(0.5)

print(f"最终合约记录数: {contract.functions.getCount().call()}")
