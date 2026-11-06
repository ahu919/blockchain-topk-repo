package main
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==================== 全局 Verbose 控制 ====================
var Verbose = true

func logDebug(format string, args ...interface{}) {
	if Verbose {
		fmt.Printf(format, args...)
	}
}

func logError(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}

// ==================== 数据结构 ====================
type Record struct {
	ID  int     `json:"id"`
	Val float64 `json:"val"`
}

type QuantileInfo struct {
	Val   float64  `json:"val"`
	Rank  int      `json:"rank"`
	ID    int      `json:"id"`
	Proof []string `json:"proof"`
}

type StatsResponse struct {
	Min, Max  float64
	Count     int
	RootHash  string
	Height    int            `json:"height"`
	Quantiles []QuantileInfo `json:"quantiles"`
}

type SubTreeSummary struct {
	Hash  string  `json:"hash"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Count int     `json:"count"`
	MinID int     `json:"minID"`
	MaxID int     `json:"maxID"`
}

type IntegrityProof struct {
	SplitPath  []string       `json:"splitPath"`
	LeftChild  SubTreeSummary `json:"leftChild"`
	RightChild SubTreeSummary `json:"rightChild"`
}

type ShardClient struct {
	baseURL    string
	id         int
	httpClient *http.Client
}

// ==================== 工具函数 ====================
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func sToF(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
func sToI(s string) int     { i, _ := strconv.Atoi(s); return i }

func computeRootFromProof(id int, val float64, hashes []string) string {
	currentHash := hashString(fmt.Sprintf("leaf|%d|%.6f", id, val))
	currentMin, currentMax := val, val
	currentMinID, currentMaxID := id, id
	currentCount := 1
	for i := len(hashes) - 1; i >= 0; i-- {
		parts := strings.Split(hashes[i], "|")
		if len(parts) < 7 {
			continue
		}
		dir, sHash := parts[0], parts[1]
		sMin := sToF(parts[2])
		sMax := sToF(parts[3])
		sCount := sToI(parts[4])
		sMinID := sToI(parts[5])
		sMaxID := sToI(parts[6])

		var leftH, rightH string
		var nMin, nMax float64
		var nMinID, nMaxID int
		if dir == "R" {
			leftH, rightH = currentHash, sHash
			nMin, nMax, nMinID, nMaxID = currentMin, sMax, currentMinID, sMaxID
		} else {
			leftH, rightH = sHash, currentHash
			nMin, nMax, nMinID, nMaxID = sMin, currentMax, sMinID, currentMaxID
		}
		nCount := currentCount + sCount
		currentHash = hashString(fmt.Sprintf("%s|%s|%.6f|%.6f|%d|%d|%d",
			leftH, rightH, nMin, nMax, nCount, nMinID, nMaxID))
		currentMin, currentMax, currentMinID, currentMaxID, currentCount = nMin, nMax, nMinID, nMaxID, nCount
	}
	return currentHash
}

// VerifyIntegrityV2 边界修复版（安全截取）
func VerifyIntegrityV2(theta float64, proof IntegrityProof, returnedRecordCount int,
	expectedRootHash string, trustedRoot string) bool {

	safePrefix := func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	}

	if expectedRootHash != trustedRoot {
		logError("  ❌ [可信根不匹配] 分片返回根(%s...) ≠ 可信状态根(%s...)\n",
			safePrefix(expectedRootHash), safePrefix(trustedRoot))
		return false
	}

	if proof.RightChild.Count == 0 && proof.LeftChild.Hash != "" {
		if returnedRecordCount != 0 {
			logError("  ❌ [边界失败] 预期无记录但返回了 %d 条\n", returnedRecordCount)
			return false
		}
		if proof.LeftChild.Max > theta {
			logError("  ❌ [边界失败] 全树 ≤ θ 但左子树最大值(%.2f) > theta(%.2f)\n", proof.LeftChild.Max, theta)
			return false
		}
		if proof.LeftChild.Hash != expectedRootHash {
			logError("  ❌ [边界失败] 根哈希不匹配 (全树 ≤ θ)\n")
			return false
		}
		return true
	}

	if proof.LeftChild.Hash == "" && proof.RightChild.Count > 0 {
		if proof.RightChild.Hash != expectedRootHash {
			logError("  ❌ [边界失败] 根哈希不匹配 (全树 > θ)\n")
			return false
		}
		if proof.RightChild.Min < theta {
			logError("  ❌ [边界失败] 全树 > θ 但右子树最小值(%.2f) < theta(%.2f)\n", proof.RightChild.Min, theta)
			return false
		}
		if returnedRecordCount != proof.RightChild.Count {
			logError("  ❌ [计数失败] 返回记录数(%d) ≠ 预期(%d)\n", returnedRecordCount, proof.RightChild.Count)
			return false
		}
		return true
	}

	if proof.LeftChild.Hash != "" && proof.LeftChild.Max > theta {
		logError("  ❌ [边界失败] 左子树最大值(%.2f) > theta(%.2f)\n", proof.LeftChild.Max, theta)
		return false
	}
	if proof.RightChild.Count > 0 && proof.RightChild.Min < theta {
		logError("  ❌ [边界失败] 右子树最小值(%.2f) < theta(%.2f)\n", proof.RightChild.Min, theta)
		return false
	}

	nMin := proof.LeftChild.Min
	nMax := proof.RightChild.Max
	nMinID := proof.LeftChild.MinID
	nMaxID := proof.RightChild.MaxID
	nCount := proof.LeftChild.Count + proof.RightChild.Count
	splitNodeHash := hashString(fmt.Sprintf("%s|%s|%.6f|%.6f|%d|%d|%d",
		proof.LeftChild.Hash, proof.RightChild.Hash, nMin, nMax, nCount, nMinID, nMaxID))

	curHash := splitNodeHash
	curMin, curMax := nMin, nMax
	curMinID, curMaxID := nMinID, nMaxID
	curCount := nCount
	expectedCount := proof.RightChild.Count

	for _, sibling := range proof.SplitPath {
		parts := strings.Split(sibling, "|")
		if len(parts) < 7 {
			continue
		}
		dir, sHash := parts[0], parts[1]
		sMin := sToF(parts[2])
		sMax := sToF(parts[3])
		sCount := sToI(parts[4])
		sMinID := sToI(parts[5])
		sMaxID := sToI(parts[6])

		var leftH, rightH string
		var newMin, newMax float64
		var newMinID, newMaxID int

		if dir == "R" {
			leftH, rightH = curHash, sHash
			newMin, newMax = curMin, sMax
			newMinID, newMaxID = curMinID, sMaxID
			expectedCount += sCount
		} else {
			leftH, rightH = sHash, curHash
			newMin, newMax = sMin, curMax
			newMinID, newMaxID = sMinID, curMaxID
		}
		newCount := curCount + sCount
		curHash = hashString(fmt.Sprintf("%s|%s|%.6f|%.6f|%d|%d|%d",
			leftH, rightH, newMin, newMax, newCount, newMinID, newMaxID))
		curMin, curMax, curMinID, curMaxID, curCount = newMin, newMax, newMinID, newMaxID, newCount
	}

	if returnedRecordCount != expectedCount {
		logError("  ❌ [计数失败] 返回记录数(%d) ≠ 预期(%d)\n", returnedRecordCount, expectedCount)
		return false
	}

	if curHash != expectedRootHash {
		logError("  ❌ [验证失败] 计算根(%s...) ≠ 期望根(%s...)\n",
			safePrefix(curHash), safePrefix(expectedRootHash))
		return false
	}
	return true
}

// ==================== HTTP 客户端 ====================
func (c *ShardClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *ShardClient) GetStats() (StatsResponse, int64, time.Duration) {
	start := time.Now()
	b, _ := c.doGet(c.baseURL + "/stats")
	elapsed := time.Since(start)
	var r StatsResponse
	json.Unmarshal(b, &r)
	return r, int64(len(b)), elapsed
}

func (c *ShardClient) GetAdaptiveStats(targetRank, globalN int) (StatsResponse, int64) {
	url := fmt.Sprintf("%s/stats?targetRank=%d&globalN=%d", c.baseURL, targetRank, globalN)
	b, _ := c.doGet(url)
	var r StatsResponse
	json.Unmarshal(b, &r)
	return r, int64(len(b))
}

func (c *ShardClient) CountQueryWithProof(t float64) (int, []string, int64) {
	b, _ := c.doGet(fmt.Sprintf("%s/count?theta=%.6f", c.baseURL, t))
	var r struct {
		Count      int      `json:"count"`
		RangeProof []string `json:"rangeProof"`
	}
	json.Unmarshal(b, &r)
	return r.Count, r.RangeProof, int64(len(b))
}

func (c *ShardClient) CountInclusiveQuery(t float64) (int, []string, int64) {
	b, _ := c.doGet(fmt.Sprintf("%s/count_inclusive?theta=%.6f", c.baseURL, t))
	var r struct {
		Count      int      `json:"count"`
		RangeProof []string `json:"rangeProof"`
	}
	json.Unmarshal(b, &r)
	return r.Count, r.RangeProof, int64(len(b))
}

func (c *ShardClient) DataInclusiveQuery(t float64) ([]Record, [][]string, string, IntegrityProof, int64) {
	b, _ := c.doGet(fmt.Sprintf("%s/data_inclusive?theta=%.6f", c.baseURL, t))
	var r struct {
		Records        []Record       `json:"records"`
		Proofs         [][]string     `json:"proofs"`
		RootHash       string         `json:"rootHash"`
		IntegrityProof IntegrityProof `json:"integrityProof"`
	}
	json.Unmarshal(b, &r)
	return r.Records, r.Proofs, r.RootHash, r.IntegrityProof, int64(len(b))
}

func (c *ShardClient) LocalTopKQuery(k int) ([]Record, [][]string, string, int64, error) {
	url := fmt.Sprintf("%s/local_topk?k=%d", c.baseURL, k)
	b, err := c.doGet(url)
	if err != nil {
		return nil, nil, "", int64(len(b)), err
	}
	var r struct {
		Records  []Record   `json:"records"`
		Proofs   [][]string `json:"proofs"`
		RootHash string     `json:"rootHash"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, "", int64(len(b)), err
	}
	return r.Records, r.Proofs, r.RootHash, int64(len(b)), nil
}

// ==================== 辅助函数 ====================
func estimateCumulative(stats StatsResponse, x float64) float64 {
	if x < stats.Min {
		return 0
	}
	if x >= stats.Max {
		return float64(stats.Count)
	}
	type pt struct{ val, cum float64 }
	pts := []pt{{stats.Min, 0}}
	for _, q := range stats.Quantiles {
		pts = append(pts, pt{q.Val, float64(q.Rank + 1)})
	}
	pts = append(pts, pt{stats.Max, float64(stats.Count)})
	sort.Slice(pts, func(i, j int) bool { return pts[i].val < pts[j].val })
	for i := 0; i < len(pts)-1; i++ {
		if x >= pts[i].val && x <= pts[i+1].val {
			if pts[i+1].val == pts[i].val {
				return pts[i].cum
			}
			return pts[i].cum + (pts[i+1].cum-pts[i].cum)*(x-pts[i].val)/(pts[i+1].val-pts[i].val)
		}
	}
	return float64(stats.Count)
}

// ===== 带耗时分解的数据收集 =====
func collectDataGuaranteeKWithTiming(clients []*ShardClient, theta float64, start time.Time, K int, gMin float64,
	trustedRoots []string) ([]Record, time.Duration, int, int64, int, bool, time.Duration, time.Duration, time.Duration) {
	tryTheta := theta
	var totalNetTime, totalExistTime, totalIntegTime time.Duration
	for retry := 0; retry <= 5; retry++ {
		var allResults []Record
		totalFetched := 0
		var totalDataBytes int64 = 0

		for i, client := range clients {
			netStart := time.Now()
			recs, proofs, root, integrity, b := client.DataInclusiveQuery(tryTheta)
			totalNetTime += time.Since(netStart)
			totalDataBytes += b

			existStart := time.Now()
			existValid := true
			for j, r := range recs {
				if computeRootFromProof(r.ID, r.Val, proofs[j]) != root {
					logError("   ❌ 分片 %d 记录 %d 存在性证明无效，已丢弃该分片\n", client.id, r.ID)
					existValid = false
					break
				}
			}
			totalExistTime += time.Since(existStart)
			if !existValid {
				continue
			}

			integStart := time.Now()
			if !VerifyIntegrityV2(tryTheta, integrity, len(recs), root, trustedRoots[i]) {
				logError("   ❌ 分片 %d 完整性验证失败，已丢弃该分片\n", client.id)
				totalIntegTime += time.Since(integStart)
				continue
			}
			totalIntegTime += time.Since(integStart)

			logDebug("   ✅ Shard %d 完整性+存在性验证通过 (%d 条记录, %d bytes)\n", client.id, len(recs), b)
			totalFetched += len(recs)
			allResults = append(allResults, recs...)
		}

		sort.Slice(allResults, func(i, j int) bool { return allResults[i].Val > allResults[j].Val })
		if len(allResults) >= K || retry == 5 || tryTheta <= gMin {
			if len(allResults) > K {
				allResults = allResults[:K]
			}
			return allResults, time.Since(start), totalFetched, totalDataBytes, len(allResults), true, totalNetTime, totalExistTime, totalIntegTime
		}
		logDebug("   ⚠️ 收集到 %d 条 < %d，降低阈值重试 (θ=%.2f -> %.2f)\n",
			len(allResults), K, tryTheta, tryTheta-1.0)
		tryTheta -= 1.0
	}
	return nil, 0, 0, 0, 0, false, totalNetTime, totalExistTime, totalIntegTime
}

// ==================== 三种方案 ====================

func baselineLocalTopK(clients []*ShardClient, K int, trustedRoots []string) ([]Record, time.Duration, int64, bool) {
	start := time.Now()
	var totalBytes int64 = 0
	var allRecords []Record

	type shardResult struct {
		records []Record
		proofs  [][]string
		root    string
		bytes   int64
		err     error
		id      int
	}
	ch := make(chan shardResult, len(clients))
	for i, client := range clients {
		go func(c *ShardClient, idx int) {
			recs, proofs, root, b, err := c.LocalTopKQuery(K)
			ch <- shardResult{recs, proofs, root, b, err, c.id}
		}(client, i)
	}

	for i := 0; i < len(clients); i++ {
		res := <-ch
		totalBytes += res.bytes
		if res.err != nil {
			logError("   ❌ 分片 %d 请求失败: %v\n", res.id, res.err)
			return nil, 0, totalBytes, false
		}
		idx := -1
		for j, c := range clients {
			if c.id == res.id {
				idx = j
				break
			}
		}
		if idx >= 0 && res.root != trustedRoots[idx] {
			logError("   ❌ 分片 %d 根哈希不匹配 (可信根检查)\n", res.id)
			return nil, 0, totalBytes, false
		}
		allOk := true
		for j, rec := range res.records {
			if computeRootFromProof(rec.ID, rec.Val, res.proofs[j]) != res.root {
				logError("   ❌ 分片 %d 记录 %d 存在性证明无效\n", res.id, rec.ID)
				allOk = false
				break
			}
		}
		if !allOk {
			return nil, 0, totalBytes, false
		}
		allRecords = append(allRecords, res.records...)
	}

	sort.Slice(allRecords, func(i, j int) bool { return allRecords[i].Val > allRecords[j].Val })
	if len(allRecords) > K {
		allRecords = allRecords[:K]
	}

	dur := time.Since(start)
	return allRecords, dur, totalBytes, true
}

func baselineBinarySearchComplete(clients []*ShardClient, low, high float64, K int, gMin float64,
	trustedRoots []string) (int, time.Duration, int64, []Record, int, bool) {
	start := time.Now()
	var totalBytes int64 = 0
	iter := 0
	var finalTheta float64

	for high-low > 0.1 && iter < 100 {
		iter++
		mid := (low + high) / 2
		strict, inclusive := 0, 0
		for _, client := range clients {
			c, _, b := client.CountQueryWithProof(mid)
			strict += c
			totalBytes += b
			c2, _, b2 := client.CountInclusiveQuery(mid)
			inclusive += c2
			totalBytes += b2
		}
		if strict < K && inclusive >= K {
			finalTheta = mid
			break
		} else if strict >= K {
			low = mid
		} else {
			high = mid
		}
	}
	if finalTheta == 0 {
		finalTheta = (low + high) / 2
	}

	results, dur, totalRecords, dataBytes, finalK, ok, _, _, _ := collectDataGuaranteeKWithTiming(
		clients, finalTheta, start, K, gMin, trustedRoots)
	if !ok {
		return iter, dur, totalBytes + dataBytes, nil, 0, false
	}
	logDebug("   [基线二分] 阈值=%.2f | 迭代=%d | 总传输=%d条 | 返回=%d条\n",
		finalTheta, iter, totalRecords, finalK)
	return iter, dur, totalBytes + dataBytes, results, finalK, true
}

func findNextQuantileBound(allStats []StatsResponse, currentBound float64, direction string) (float64, bool) {
	var candidate *float64
	for _, stats := range allStats {
		for _, q := range stats.Quantiles {
			if direction == "right" {
				if q.Val > currentBound && (candidate == nil || q.Val < *candidate) {
					v := q.Val
					candidate = &v
				}
			} else {
				if q.Val < currentBound && (candidate == nil || q.Val > *candidate) {
					v := q.Val
					candidate = &v
				}
			}
		}
	}
	if candidate != nil {
		return *candidate, true
	}
	return 0, false
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// RAQP 协议（支持环境变量控制的消融模式）
func runQueryWithBytes(K int, clients []*ShardClient, allStats []StatsResponse, gMin, gMax float64, totalN int,
	fallbackResults []Record, fallbackDur time.Duration, fallbackBytes int64,
	trustedRoots []string) ([]Record, time.Duration, int, int64, bool, time.Duration, time.Duration, time.Duration, time.Duration) {

	start := time.Now()
	var totalCountBytes int64 = 0
	if K > totalN {
		K = totalN
	}
	targetRank := totalN - K
	logDebug("\n🎯 K=%d | R_target=%d\n", K, targetRank)

	searchStart := time.Now()

	// ==================== 消融模式控制 ====================
	ablationNoNarrow := os.Getenv("ABLATION_NO_NARROW_INTERVAL") == "1"
	ablationNoDual := os.Getenv("ABLATION_NO_DUAL_BOUNDARY") == "1"
	if ablationNoNarrow {
		logDebug("   ⚠️ [消融 2] 跳过窄区间估计，用采样点极值作为初始区间\n")
	}
	if ablationNoDual {
		logDebug("   ⚠️ [消融 3] 使用单边界搜索\n")
	}
	// ======================================================

	// 阶段 1：自适应分位数收集（所有模式下都保留）
	honestClients := []*ShardClient{}
	honestStats := []StatsResponse{}
	honestRoots := []string{}
	for _, client := range clients {
		stats, b := client.GetAdaptiveStats(targetRank, totalN)
		totalCountBytes += b

		allOk := true
		for _, q := range stats.Quantiles {
			if computeRootFromProof(q.ID, q.Val, q.Proof) != stats.RootHash {
				logError("   ❌ 分片 %d 存在性证明失败！(rank=%d, val=%.2f) 已排除该分片\n",
					client.id, q.Rank, q.Val)
				allOk = false
				break
			}
		}
		if !allOk {
			continue
		}
		honestStats = append(honestStats, stats)
		honestClients = append(honestClients, client)
		for i, c := range clients {
			if c.id == client.id {
				honestRoots = append(honestRoots, trustedRoots[i])
				break
			}
		}
	}
	if len(honestClients) == 0 {
		logError("  ❌ 所有分片均被排除，查询失败\n")
		return nil, 0, 0, 0, false, 0, 0, 0, 0
	}
	logDebug("   ✅ 诚实分片数量: %d\n", len(honestClients))
	clients = honestClients
	trustedRoots = honestRoots
	allStats = honestStats

	actualTotalN := 0
	for _, s := range allStats {
		actualTotalN += s.Count
	}
	if K > actualTotalN {
		K = actualTotalN
	}
	targetRank = actualTotalN - K

	// ==================== 阶段 2：窄区间估计（消融 2 跳过 CDF 构建） ====================
	var theta, leftBound, rightBound, localSpan float64

	if ablationNoNarrow {
		// 消融 2：跳过 CDF 构建，直接用采样点的最小/最大值作为初始区间
		minVal := gMax
		maxVal := gMin
		for _, stats := range allStats {
			for _, q := range stats.Quantiles {
				if q.Val < minVal {
					minVal = q.Val
				}
				if q.Val > maxVal {
					maxVal = q.Val
				}
			}
		}
		leftBound = minVal
		rightBound = maxVal
		localSpan = rightBound - leftBound
		theta = (leftBound + rightBound) / 2
		logDebug("  [消融 2] 采样点极值区间 [%.0f, %.0f] 宽度%.0f\n", leftBound, rightBound, localSpan)
	} else {
		// 原逻辑：构建全局 CDF
		type globalPoint struct{ val, cumEst float64 }
		var points []globalPoint
		for _, stats := range allStats {
			for _, q := range stats.Quantiles {
				est := 0.0
				for _, os := range allStats {
					est += estimateCumulative(os, q.Val)
				}
				points = append(points, globalPoint{q.Val, est})
			}
		}
		points = append(points, globalPoint{gMin, 0.0}, globalPoint{gMax, float64(actualTotalN)})
		sort.Slice(points, func(i, j int) bool { return points[i].val < points[j].val })
		unique := []globalPoint{}
		for i, p := range points {
			if i == 0 || p.val != points[i-1].val {
				unique = append(unique, p)
			} else if p.cumEst > unique[len(unique)-1].cumEst {
				unique[len(unique)-1] = p
			}
		}
		points = unique

		target := float64(targetRank)
		for i := 0; i < len(points)-1; i++ {
			if points[i].cumEst <= target && target <= points[i+1].cumEst {
				leftBound = points[i].val
				rightBound = points[i+1].val
				localSpan = rightBound - leftBound
				if points[i+1].cumEst == points[i].cumEst {
					theta = points[i].val
				} else {
					ratio := (target - points[i].cumEst) / (points[i+1].cumEst - points[i].cumEst)
					theta = points[i].val + ratio*(points[i+1].val-points[i].val)
				}
				break
			}
		}
		logDebug("  初始区间 [%.0f, %.0f] 宽度%.0f θ=%.2f\n", leftBound, rightBound, localSpan, theta)
	}

	originalSpan := localSpan
	if originalSpan < 1 {
		originalSpan = 1
	}

	// ==================== 阶段 3：微调（消融 3 切换到单边界） ====================
	var totalRounds int
	const intervalMaxRounds = 30

	if ablationNoDual {
		// ========== 消融 3：单边界搜索 ==========
		// 只用单个 θ，两个条件作用在同一个变量上
		// 不加任何启发式，让单边界自然震荡
		thetaSingle := (leftBound + rightBound) / 2
		jump := originalSpan
		if jump < 1 {
			jump = 1
		}

		for round := 1; round <= intervalMaxRounds; round++ {
			cStrict := 0
			cIncl := 0
			for _, client := range clients {
				c, _, b := client.CountQueryWithProof(thetaSingle)
				cStrict += c
				totalCountBytes += b
				c2, _, b2 := client.CountInclusiveQuery(thetaSingle)
				cIncl += c2
				totalCountBytes += b2
			}
			totalRounds++

			logDebug("   [消融 3] 轮%d θ=%.2f C>=%d C>=%d\n", round, thetaSingle, cStrict, cIncl)

			if cStrict < K && cIncl >= K {
				logDebug("   ✅ [消融 3] 单边界收敛 (轮次=%d)\n", totalRounds)
				searchTime := time.Since(searchStart)
				results, dur, _, dataB, _, ok, netT, existT, integT := collectDataGuaranteeKWithTiming(
					clients, thetaSingle, start, K, gMin, trustedRoots)
				return results, dur, totalRounds, totalCountBytes + dataB, ok, searchTime, netT, existT, integT
			}

			if cStrict >= K {
				// C> 太多，θ 太小，向上跳
				thetaSingle += jump
			} else {
				// C≥ 不足，θ 太大，向下跳
				thetaSingle -= jump
			}
			jump *= 2

			if thetaSingle < gMin {
				thetaSingle = gMin
			}
			if thetaSingle > gMax {
				thetaSingle = gMax
			}
		}
	} else {
		// ========== 完整版：双边界搜索 ==========
		thetaLow := leftBound
		thetaHigh := rightBound
		if thetaHigh <= thetaLow {
			thetaHigh = thetaLow + 1
		}
		jumpLow := originalSpan
		jumpHigh := originalSpan
		if jumpLow < 1 {
			jumpLow = 1
		}
		if jumpHigh < 1 {
			jumpHigh = 1
		}

		for round := 1; round <= intervalMaxRounds; round++ {
			cLowIncl := 0
			cHighStrict := 0
			for _, client := range clients {
				c, _, b := client.CountQueryWithProof(thetaHigh)
				cHighStrict += c
				totalCountBytes += b
				c2, _, b2 := client.CountInclusiveQuery(thetaLow)
				cLowIncl += c2
				totalCountBytes += b2
			}
			totalRounds++

			logDebug("   轮%d low=%.2f(C≥=%d) high=%.2f(C>=%d)\n",
				round, thetaLow, cLowIncl, thetaHigh, cHighStrict)

			if cLowIncl >= K && cHighStrict < K {
				logDebug("   ✅ 区间收敛 (轮次=%d)\n", totalRounds)
				searchTime := time.Since(searchStart)
				results, dur, _, dataB, _, ok, netT, existT, integT := collectDataGuaranteeKWithTiming(
					clients, thetaLow, start, K, gMin, trustedRoots)
				return results, dur, totalRounds, totalCountBytes + dataB, ok, searchTime, netT, existT, integT
			}

			if cHighStrict >= K {
				thetaHigh += jumpHigh
				if thetaHigh > gMax {
					thetaHigh = gMax
				}
				jumpHigh *= 2
				if jumpHigh > (gMax-gMin)/2 {
					jumpHigh = (gMax - gMin) / 2
				}
				logDebug("   ↕ thetaHigh 上移至 %.2f\n", thetaHigh)
			}

			if cLowIncl < K {
				thetaLow -= jumpLow
				if thetaLow < gMin {
					thetaLow = gMin
				}
				jumpLow *= 2
				if jumpLow > (gMax-gMin)/2 {
					jumpLow = (gMax - gMin) / 2
				}
				logDebug("   ↕ thetaLow 下移至 %.2f\n", thetaLow)
			}

			if thetaLow >= thetaHigh {
				thetaLow = thetaHigh - 1
				if thetaLow < gMin {
					thetaLow = gMin
				}
			}
		}
	}

	// ===== 回退分支 =====
	logDebug("   ⬅️ 未收敛，回退全局二分法\n")
	_, _, bytes, results, _, ok := baselineBinarySearchComplete(
		clients, gMin, gMax, K, gMin, trustedRoots)
	if !ok {
		return nil, 0, -1, 0, false, 0, 0, 0, 0
	}
	totalDur := time.Since(start)
	searchTime := time.Since(searchStart)
	return results, totalDur, -1, totalCountBytes + bytes, true, searchTime, 0, 0, 0
}

// ==================== 吞吐量实验函数 ====================
func runThroughputExperiment(
	scheme string,
	clients []*ShardClient,
	allStats []StatsResponse,
	gMin, gMax float64,
	totalN int,
	trustedRoots []string,
	concurrency int,
	duration time.Duration,
) (completed int, qps float64, avgLatency float64, p99Latency float64, totalBytes int64) {

	Ks := []int{5, 10, 20, 50, 100, 200, 500, 1000}
	queryPool := make([]int, 1000)
	for i := range queryPool {
		queryPool[i] = Ks[rand.Intn(len(Ks))]
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var latencies []float64

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				K := queryPool[rand.Intn(len(queryPool))]
				qStart := time.Now()

				var ok bool
				var dataBytes int64

				switch scheme {
				case "raqp":
					_, _, _, dataBytes, ok, _, _, _, _ = runQueryWithBytes(K, clients, allStats,
						gMin, gMax, totalN, nil, 0, 0, trustedRoots)
				case "binary":
					_, _, dataBytes, _, _, ok = baselineBinarySearchComplete(
						clients, gMin, gMax, K, gMin, trustedRoots)
				case "naive":
					_, _, dataBytes, ok = baselineLocalTopK(clients, K, trustedRoots)
				default:
					continue
				}

				elapsed := time.Since(qStart).Seconds()
				mu.Lock()
				if ok {
					completed++
					latencies = append(latencies, elapsed)
				}
				totalBytes += dataBytes
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start).Seconds()

	if completed > 0 {
		qps = float64(completed) / elapsed
		var sum float64
		for _, l := range latencies {
			sum += l
		}
		avgLatency = sum / float64(len(latencies))
		sort.Float64s(latencies)
		idx := int(float64(len(latencies)) * 0.99)
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		p99Latency = latencies[idx]
	}

	return
}

func printThroughputResults(scheme string, concurrencyLevels []int,
	clients []*ShardClient, allStats []StatsResponse, gMin, gMax float64,
	totalN int, trustedRoots []string) {

	fmt.Printf("\n\n========== 吞吐量实验: %s ==========\n", scheme)
	fmt.Printf("%-12s %-12s %-12s %-12s %-12s\n", "并发", "完成数", "QPS", "平均延迟(s)", "P99延迟(s)")

	for _, c := range concurrencyLevels {
		completed, qps, avgLat, p99Lat, _ := runThroughputExperiment(
			scheme, clients, allStats, gMin, gMax, totalN, trustedRoots, c, 120*time.Second)
		fmt.Printf("%-12d %-12d %-12.2f %-12.3f %-12.3f\n",
			c, completed, qps, avgLat, p99Lat)
	}
}

// ==================== main ====================
func main() {
	rand.Seed(time.Now().UnixNano())

	mode := flag.String("mode", "default", "运行模式: default 或 throughput")
	flag.Parse()

	if *mode == "throughput" {
		Verbose = false
	} else {
		Verbose = true
	}

	fmt.Println("🚀 RAQP 协调者 (区间阈值搜索版，完整性验证 V2)")

	httpClient := &http.Client{Timeout: 30 * time.Second}

	clients := []*ShardClient{
		{baseURL: "http://127.0.0.1:8081", id: 1, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8082", id: 2, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8083", id: 3, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8084", id: 4, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8085", id: 5, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8086", id: 6, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8087", id: 7, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8088", id: 8, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8089", id: 9, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8090", id: 10, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8091", id: 11, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8092", id: 12, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8093", id: 13, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8094", id: 14, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8095", id: 15, httpClient: httpClient},
		{baseURL: "http://127.0.0.1:8096", id: 16, httpClient: httpClient},
	}

	var gMin, gMax = 1e18, -1e18
	totalN := 0
	allStats := make([]StatsResponse, len(clients))
	heightCount := make(map[int]int)
	trustedRoots := make([]string, len(clients))

	fmt.Println("\n=== 阶段0：基础统计与高度锁定 ===")
	tStage0Start := time.Now()
	var totalFreshnessTime time.Duration
	var activeShardCount int

	for i, client := range clients {
		stats, _, freshDur := client.GetStats()
		if stats.Count == 0 {
			fmt.Printf("  Shard %d 不可用，已跳过\n", client.id)
			continue
		}
		activeShardCount++
		totalFreshnessTime += freshDur
		allStats[i] = stats
		trustedRoots[i] = stats.RootHash
		heightCount[stats.Height]++
		if stats.Min < gMin {
			gMin = stats.Min
		}
		if stats.Max > gMax {
			gMax = stats.Max
		}
		totalN += stats.Count
		fmt.Printf("  Shard %d: min=%.0f max=%.0f count=%d height=%d root=%s\n",
			client.id, stats.Min, stats.Max, stats.Count, stats.Height, stats.RootHash[:12])
	}

	queryHeight := 0
	maxCnt := 0
	for h, cnt := range heightCount {
		if cnt > maxCnt {
			maxCnt = cnt
			queryHeight = h
		}
	}
	tStage0Total := time.Since(tStage0Start)
	avgFreshnessTime := totalFreshnessTime / time.Duration(activeShardCount)
	fmt.Printf("  全局: min=%.0f max=%.0f total=%d | 锁定查询高度: %d\n", gMin, gMax, totalN, queryHeight)
	fmt.Printf("  阶段0总耗时: %v (含所有分片统计请求)\n", tStage0Total)
	fmt.Printf("  新鲜性验证（单分片平均请求耗时）: %v\n", avgFreshnessTime)

	if *mode == "throughput" {
		concurrencyLevels := []int{1, 2, 4, 8}
		printThroughputResults("raqp", concurrencyLevels, clients, allStats, gMin, gMax, totalN, trustedRoots)
		printThroughputResults("binary", concurrencyLevels, clients, allStats, gMin, gMax, totalN, trustedRoots)
		printThroughputResults("naive", concurrencyLevels, clients, allStats, gMin, gMax, totalN, trustedRoots)
		return
	}

	for _, K := range []int{10, 50, 100, 200, 500, 1000} {
		fmt.Printf("\n===== K=%d (基于高度 %d) =====\n", K, queryHeight)

		bIter, bDur, bBytes, bResults, bReturned, ok := baselineBinarySearchComplete(
			clients, gMin, gMax, K, gMin, trustedRoots)
		if !ok {
			fmt.Println("  ❌ 基线二分法验证失败")
			continue
		}
		fmt.Printf("  [基线二分] 轮次=%d 耗时=%v 通信=%d bytes 收集=%d 返回=%d\n",
			bIter, bDur, bBytes, len(bResults), bReturned)

		results, dur, pliRounds, pliBytes, ok, searchTime, netTime, existTime, integTime := runQueryWithBytes(
			K, clients, allStats, gMin, gMax, totalN, bResults, bDur, bBytes, trustedRoots)
		if !ok {
			fmt.Printf("  ❌ RAQP 验证失败\n")
			continue
		}
		status := "区间收敛"
		if pliRounds == -1 {
			status = "回退二分"
		}
		fmt.Printf("  [RAQP] %s 轮次=%d 耗时=%v 通信=%d bytes 返回=%d\n",
			status, pliRounds, dur, pliBytes, len(results))
		if len(results) > 0 {
			fmt.Print("    Top5: ")
			for i := 0; i < len(results) && i < 5; i++ {
				fmt.Printf("(%d,%.0f) ", results[i].ID, results[i].Val)
			}
			fmt.Println()
		}
		fmt.Printf("  阶段耗时分解 (K=%d):\n", K)
		fmt.Printf("    阈值搜索 (含自适应采样+微调): %v\n", searchTime)
		fmt.Printf("    数据收集 (各分片网络传输):     %v\n", netTime)
		fmt.Printf("    存在性验证 (记录默克尔路径):   %v\n", existTime)
		fmt.Printf("    完整性验证 (V2证明校验):       %v\n", integTime)
		fmt.Printf("    新鲜性验证 (单分片平均请求):     %v\n", avgFreshnessTime)

		fmt.Println("\n  --- 朴素本地 Top-K 方案 (Baseline 3) ---")
		naiveResults, naiveDur, naiveBytes, naiveOk := baselineLocalTopK(clients, K, trustedRoots)
		if !naiveOk {
			fmt.Println("  ❌ 朴素方案验证失败")
		} else {
			fmt.Printf("  [朴素Top-K] 耗时=%v 通信=%d bytes 返回=%d\n", naiveDur, naiveBytes, len(naiveResults))
			if len(naiveResults) > 0 {
				fmt.Print("    Top5: ")
				for i := 0; i < len(naiveResults) && i < 5; i++ {
					fmt.Printf("(%d,%.0f) ", naiveResults[i].ID, naiveResults[i].Val)
				}
				fmt.Println()
			}
		}
	}
}
