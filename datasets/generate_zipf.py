import numpy as np
import csv

NUM_SHARDS = 16
RECORDS_PER_SHARD = 38000
ALPHA = 1.2
MAX_VAL = 95000

def save_csv(data, filename):
    with open(filename, 'w', newline='') as f:
        writer = csv.writer(f)
        writer.writerow(['id', 'val'])
        for i, val in enumerate(data):
            writer.writerow([i + 1, int(val)])

for shard in range(1, NUM_SHARDS + 1):
    np.random.seed(shard)
    # 截断 Zipf 分布，保证取到足够的合法值
    raw = np.random.zipf(ALPHA, int(RECORDS_PER_SHARD * 3))
    valid = raw[raw <= MAX_VAL][:RECORDS_PER_SHARD]
    save_csv(valid, f'shard_{shard}_zipf.csv')
    print(f'Shard {shard}: {len(valid)} records, range [{valid.min()}, {valid.max()}]')
