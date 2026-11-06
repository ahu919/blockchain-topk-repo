import csv
import hashlib
import os

def split_uniform(input_file, output_dir="shards_16", num_shards=16):
    os.makedirs(output_dir, exist_ok=True)
    shard_files = []
    shard_writers = []

    for i in range(num_shards):
        f = open(os.path.join(output_dir, f"shard_{i}.csv"), "w")
        f.write("id,value\n")
        shard_files.append(f)
        shard_writers.append(csv.writer(f))

    with open(input_file, 'r') as infile:
        reader = csv.reader(infile, delimiter='|')
        for idx, row in enumerate(reader):
            if len(row) < 5:
                continue
            orderkey = row[0]
            price = int(float(row[5]))
            # 使用哈希保证均匀
            h = hashlib.sha256(orderkey.encode()).hexdigest()
            shard_id = int(h, 16) % num_shards
            shard_writers[shard_id].writerow([orderkey, price])

    for f in shard_files:
        f.close()

    print("完成，各分片记录数统计：")
    for i in range(num_shards):
        with open(os.path.join(output_dir, f"shard_{i}.csv")) as f:
            count = sum(1 for _ in f) - 1
        print(f"分片 {i}: {count} 行")

if __name__ == "__main__":
    split_uniform("dbgen_src/lineitem.tbl")
