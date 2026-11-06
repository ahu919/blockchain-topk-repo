package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
)

// -------------------- 数据结构 --------------------
type Record struct {
	ID  int     `json:"id"`
	Val float64 `json:"val"`
}

type MITNode struct {
	Min, Max    float64
	MinID, MaxID int
	Count       int
	Left, Right *MITNode
	Hash        string
	LeafRecord  *Record
}

func (n *MITNode) IsLeaf() bool {
	return n.Left == nil && n.Right == nil
}

type MIT struct {
	Root   *MITNode
	Values []Record
}

// 统一的子树摘要
type SubTreeSummary struct {
	Hash  string  `json:"hash"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Count int     `json:"count"`
	MinID int     `json:"minID"`
	MaxID int     `json:"maxID"`
}

// 完整性证明结构（支持根节点分割）
type IntegrityProof struct {
	SplitPath  []string       `json:"splitPath"`
	LeftChild  SubTreeSummary `json:"leftChild"`
	RightChild SubTreeSummary `json:"rightChild"`
}

// -------------------- 哈希与树构建 --------------------
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func NewMIT(records []Record) *MIT {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Val == records[j].Val {
			return records[i].ID < records[j].ID
		}
		return records[i].Val < records[j].Val
	})
	mit := &MIT{Values: records}
	mit.Root = mit.buildTree(0, len(records)-1)
	return mit
}

func (mit *MIT) buildTree(l, r int) *MITNode {
	if l > r {
		return nil
	}
	if l == r {
		rec := mit.Values[l]
		node := &MITNode{
			Min: rec.Val, Max: rec.Val,
			MinID: rec.ID, MaxID: rec.ID,
			Count: 1, LeafRecord: &rec,
		}
		node.Hash = hashString(fmt.Sprintf("leaf|%d|%.6f", rec.ID, rec.Val))
		return node
	}
	mid := (l + r) / 2
	left := mit.buildTree(l, mid)
	right := mit.buildTree(mid+1, r)

	node := &MITNode{
		Min:   left.Min,
		Max:   right.Max,
		MinID: left.MinID,
		MaxID: right.MaxID,
		Count: left.Count + right.Count,
		Left:  left,
		Right: right,
	}
	// 拼接顺序：左哈希|右哈希|Min|Max|Count|MinID|MaxID
	node.Hash = hashString(fmt.Sprintf("%s|%s|%.6f|%.6f|%d|%d|%d",
		left.Hash, right.Hash, node.Min, node.Max, node.Count, node.MinID, node.MaxID))
	return node
}

// -------------------- 存在性证明 --------------------
func (mit *MIT) proveExistence(node *MITNode, id int, val float64, hashes *[]string) bool {
	if node.Left == nil && node.Right == nil {
		return node.LeafRecord != nil && node.LeafRecord.ID == id && node.LeafRecord.Val == val
	}
	if val < node.Left.Max || (val == node.Left.Max && id <= node.Left.MaxID) {
		s := node.Right
		*hashes = append(*hashes, fmt.Sprintf("R|%s|%.6f|%.6f|%d|%d|%d",
			s.Hash, s.Min, s.Max, s.Count, s.MinID, s.MaxID))
		return mit.proveExistence(node.Left, id, val, hashes)
	} else {
		s := node.Left
		*hashes = append(*hashes, fmt.Sprintf("L|%s|%.6f|%.6f|%d|%d|%d",
			s.Hash, s.Min, s.Max, s.Count, s.MinID, s.MaxID))
		return mit.proveExistence(node.Right, id, val, hashes)
	}
}

func (mit *MIT) GetQuantile(rank int) (Record, []string) {
	if rank < 0 || rank >= len(mit.Values) {
		return Record{}, nil
	}
	rec := mit.Values[rank]
	proof := []string{}
	mit.proveExistence(mit.Root, rec.ID, rec.Val, &proof)
	return rec, proof
}
func (mit *MIT) GetLocalTopK(k int) ([]Record, [][]string) {
    n := len(mit.Values)
    if k > n {
        k = n
    }
    records := make([]Record, 0, k)
    proofs := make([][]string, 0, k)
    for rank := n - 1; rank >= n - k; rank-- {
        rec, proof := mit.GetQuantile(rank)
        records = append(records, rec)
        proofs = append(proofs, proof)
    }
    return records, proofs
}

// -------------------- 范围查询 --------------------
func (mit *MIT) DataQueryGreaterOrEqual(theta float64) ([]Record, [][]string) {
	var records []Record
	var proofs [][]string
	for _, r := range mit.Values {
		if r.Val >= theta {
			records = append(records, r)
			p := []string{}
			mit.proveExistence(mit.Root, r.ID, r.Val, &p)
			proofs = append(proofs, p)
		}
	}
	return records, proofs
}

func (mit *MIT) CountGreaterWithProof(theta float64) (int, []string) {
	var proof []string
	count := mit.countRecursive(mit.Root, theta, &proof, false)
	return count, proof
}

func (mit *MIT) CountGreaterOrEqualWithProof(theta float64) (int, []string) {
	var proof []string
	count := mit.countRecursive(mit.Root, theta, &proof, true)
	return count, proof
}

func (mit *MIT) countRecursive(node *MITNode, theta float64, proof *[]string, inclusive bool) int {
	if node == nil {
		return 0
	}
	condFull := node.Min > theta
	condPrune := node.Max <= theta
	if inclusive {
		condFull = node.Min >= theta
		condPrune = node.Max < theta
	}
	if condFull {
		*proof = append(*proof, fmt.Sprintf("FULL|%s|%.6f|%.6f|%d|%d|%d",
			node.Hash, node.Min, node.Max, node.Count, node.MinID, node.MaxID))
		return node.Count
	}
	if condPrune {
		*proof = append(*proof, fmt.Sprintf("PRUNE|%s|%.6f|%.6f|%d|%d|%d",
			node.Hash, node.Min, node.Max, node.Count, node.MinID, node.MaxID))
		return 0
	}
	return mit.countRecursive(node.Left, theta, proof, inclusive) +
		mit.countRecursive(node.Right, theta, proof, inclusive)
}

// -------------------- 完整性证明（修正版）--------------------
// 分割节点定义：左子树所有值 < theta，右子树所有值 >= theta
func (mit *MIT) buildIntegrityPath(node *MITNode, theta float64, proof *IntegrityProof) []string {
	if node == nil || node.IsLeaf() {
		return nil
	}

	// 检查当前节点是否为“分割节点”
	// 条件从原来的 theta >= Left.Max && theta < Right.Min
	// 修改为：Left.Max < theta && Right.Min >= theta
	if node.Left.Max < theta && node.Right.Min >= theta {
		proof.LeftChild = SubTreeSummary{
			Hash: node.Left.Hash, Min: node.Left.Min, Max: node.Left.Max,
			Count: node.Left.Count, MinID: node.Left.MinID, MaxID: node.Left.MaxID,
		}
		proof.RightChild = SubTreeSummary{
			Hash: node.Right.Hash, Min: node.Right.Min, Max: node.Right.Max,
			Count: node.Right.Count, MinID: node.Right.MinID, MaxID: node.Right.MaxID,
		}
		return []string{}
	}

	// 走向判断：如果 theta <= Left.Max，说明左子树里还有 >= theta 的记录，向左走
	if theta <= node.Left.Max {
		subPath := mit.buildIntegrityPath(node.Left, theta, proof)
		if subPath != nil {
			s := node.Right
			return append(subPath, fmt.Sprintf("R|%s|%.6f|%.6f|%d|%d|%d",
				s.Hash, s.Min, s.Max, s.Count, s.MinID, s.MaxID))
		}
	} else {
		// 否则 theta > Left.Max，左子树全部 < theta，分割点应在右子树
		subPath := mit.buildIntegrityPath(node.Right, theta, proof)
		if subPath != nil {
			s := node.Left
			return append(subPath, fmt.Sprintf("L|%s|%.6f|%.6f|%d|%d|%d",
				s.Hash, s.Min, s.Max, s.Count, s.MinID, s.MaxID))
		}
	}
	return nil
}

func (mit *MIT) ProveIntegrity(theta float64) IntegrityProof {
	var proof IntegrityProof

	if mit.Root == nil {
		return proof
	}

	
	if mit.Root.Max < theta {
		proof.LeftChild = SubTreeSummary{
			Hash: mit.Root.Hash, Min: mit.Root.Min, Max: mit.Root.Max,
			Count: mit.Root.Count, MinID: mit.Root.MinID, MaxID: mit.Root.MaxID,
		}
		proof.RightChild = SubTreeSummary{Count: 0}
		return proof
	}

	
	if mit.Root.Min >= theta {
		proof.RightChild = SubTreeSummary{
			Hash: mit.Root.Hash, Min: mit.Root.Min, Max: mit.Root.Max,
			Count: mit.Root.Count, MinID: mit.Root.MinID, MaxID: mit.Root.MaxID,
		}
		proof.LeftChild = SubTreeSummary{Hash: ""}
		return proof
	}

	// 正常情况：寻找分割点
	path := mit.buildIntegrityPath(mit.Root, theta, &proof)
	if path != nil {
		proof.SplitPath = path
	}

	return proof
}

// -------------------- HTTP 服务 --------------------
type ShardService struct {
	mit    *MIT
	height int
}

const delta = 5

func (s *ShardService) StatsHandler(w http.ResponseWriter, r *http.Request) {
	c := len(s.mit.Values)
	targetRankStr := r.URL.Query().Get("targetRank")
	globalNStr := r.URL.Query().Get("globalN")

	resp := map[string]interface{}{
		"min":      s.mit.Values[0].Val,
		"max":      s.mit.Values[c-1].Val,
		"count":    c,
		"rootHash": s.mit.Root.Hash,
		"height":   s.height,
	}

	if targetRankStr != "" && globalNStr != "" {
		targetRank, _ := strconv.Atoi(targetRankStr)
		globalN, _ := strconv.Atoi(globalNStr)
		if globalN > 0 {
			localRank := int(math.Round(float64(targetRank) * float64(c) / float64(globalN)))
			start := localRank - delta
			if start < 0 {
				start = 0
			}
			end := localRank + delta
			if end >= c {
				end = c - 1
			}

			quantiles := make([]map[string]interface{}, 0, end-start+1)
			for rank := start; rank <= end; rank++ {
				rec, proof := s.mit.GetQuantile(rank)
				quantiles = append(quantiles, map[string]interface{}{
					"val":   rec.Val,
					"rank":  rank,
					"id":    rec.ID,
					"proof": proof,
				})
			}

			if os.Getenv("MALICIOUS") == "1" {
				for i := range quantiles {
					if rk, ok := quantiles[i]["rank"].(int); ok && rk == localRank {
						quantiles[i]["val"] = quantiles[i]["val"].(float64) + 50.0
						break
					}
				}
			}

			resp["quantiles"] = quantiles
		}
	}
	json.NewEncoder(w).Encode(resp)
}
func (s *ShardService) LocalTopKHandler(w http.ResponseWriter, r *http.Request) {
    kStr := r.URL.Query().Get("k")
    k, err := strconv.Atoi(kStr)
    if err != nil || k <= 0 {
        http.Error(w, "invalid k", http.StatusBadRequest)
        return
    }
    recs, proofs := s.mit.GetLocalTopK(k)
    resp := map[string]interface{}{
        "records":  recs,
        "proofs":   proofs,
        "rootHash": s.mit.Root.Hash,
        "height":   s.height,
    }
    json.NewEncoder(w).Encode(resp)
}
func (s *ShardService) CountHandler(w http.ResponseWriter, r *http.Request) {
	theta, _ := strconv.ParseFloat(r.URL.Query().Get("theta"), 64)
	count, rangeProof := s.mit.CountGreaterWithProof(theta)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count":      count,
		"rangeProof": rangeProof,
	})
}

func (s *ShardService) CountInclusiveHandler(w http.ResponseWriter, r *http.Request) {
	theta, _ := strconv.ParseFloat(r.URL.Query().Get("theta"), 64)
	count, rangeProof := s.mit.CountGreaterOrEqualWithProof(theta)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count":      count,
		"rangeProof": rangeProof,
	})
}

func (s *ShardService) DataInclusiveHandler(w http.ResponseWriter, r *http.Request) {
	theta, _ := strconv.ParseFloat(r.URL.Query().Get("theta"), 64)
	recs, proofs := s.mit.DataQueryGreaterOrEqual(theta)

//	if os.Getenv("MALICIOUS") == "1" && len(recs) > 6 {
//		recs = recs[:len(recs)-5]
//		proofs = proofs[:len(proofs)-5]
//	}

	integrityProof := s.mit.ProveIntegrity(theta)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"records":        recs,
		"proofs":         proofs,
		"rootHash":       s.mit.Root.Hash,
		"integrityProof": integrityProof,
		"height":         s.height,
	})
}

// -------------------- 从合约加载记录 --------------------
func loadRecordsFromContract(contractAddr string, rpcURL string) ([]Record, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	contract := common.HexToAddress(contractAddr)
	countData, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &contract,
		Data: hexutil.MustDecode("0xa87d942c"),
	}, nil)
	if err != nil {
		return nil, err
	}
	count := new(big.Int).SetBytes(countData).Uint64()
	log.Printf("合约中共有 %d 条记录", count)

	var records []Record
	methodID := hexutil.MustDecode("0x34461067")
	for i := uint64(0); i < count; i++ {
		param := common.LeftPadBytes(new(big.Int).SetUint64(i).Bytes(), 32)
		data := append(methodID, param...)
		result, err := client.CallContract(context.Background(), ethereum.CallMsg{
			To:   &contract,
			Data: data,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get record %d: %v", i, err)
		}
		if len(result) < 64 {
			return nil, fmt.Errorf("invalid result length for record %d", i)
		}
		id := new(big.Int).SetBytes(result[0:32]).Uint64()
		val := new(big.Int).SetBytes(result[32:64]).Uint64()
		records = append(records, Record{ID: int(id), Val: float64(val)})
		if (i+1)%100 == 0 {
			log.Printf("已加载 %d 条记录", i+1)
		}
	}
	return records, nil
}

func main() {
	contractAddr := os.Getenv("CONTRACT_ADDR")
	if contractAddr == "" {
		contractAddr = ""
	}
	rpcURL := os.Getenv("RPC_URL")
	if rpcURL == "" {
		rpcURL = ""
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = ""
	}

	records, err := loadRecordsFromContract(contractAddr, rpcURL)
	if err != nil {
		log.Fatalf("加载合约数据失败: %v", err)
	}
	log.Printf("共加载 %d 条记录", len(records))

	client, _ := ethclient.Dial(rpcURL)
	header, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		log.Fatalf("获取区块高度失败: %v", err)
	}
	currentHeight := int(header.Number.Int64())
	log.Printf("当前区块高度: %d", currentHeight)

	mit := NewMIT(records)
	service := &ShardService{mit: mit, height: currentHeight}

	http.HandleFunc("/stats", service.StatsHandler)
	http.HandleFunc("/count", service.CountHandler)
	http.HandleFunc("/count_inclusive", service.CountInclusiveHandler)
	http.HandleFunc("/data_inclusive", service.DataInclusiveHandler)
	http.HandleFunc("/local_topk", service.LocalTopKHandler)
	log.Printf("分片服务启动于 %s，高度 %d", port, currentHeight)
	log.Fatal(http.ListenAndServe(port, nil))
}
