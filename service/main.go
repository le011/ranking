package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	leaderboardKey       = "game:leaderboard:dense_rank_test" // 使用一个独立的key
	scoreMultiplier      = 1e12
	maxTimestampReversed = 1e12
)

// RankInfo 结构体保持不变
type RankInfo struct {
	PlayerID string `json:"playerId"`
	Score    int64  `json:"score"`
	Rank     int64  `json:"rank"`
}

// LeaderboardService 结构体保持不变
type LeaderboardService struct {
	rdb *redis.Client
	ctx context.Context
}

// NewLeaderboardService 构造函数保持不变
func NewLeaderboardService(rdb *redis.Client) *LeaderboardService {
	return &LeaderboardService{
		rdb: rdb,
		ctx: context.Background(),
	}
}

// UpdateScore 方法保持不变
func (s *LeaderboardService) UpdateScore(playerID string, incrScore int64, timestamp int64) error {
	oldCombinedScore, err := s.rdb.ZScore(s.ctx, leaderboardKey, playerID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	oldScore := int64(oldCombinedScore / scoreMultiplier)
	newScore := oldScore + incrScore
	newCombinedScore := float64(newScore*scoreMultiplier + (maxTimestampReversed - timestamp))
	_, err = s.rdb.ZAdd(s.ctx, leaderboardKey, redis.Z{
		Score:  newCombinedScore,
		Member: playerID,
	}).Result()
	return err
}

// GetTopN 方法保持不变 (用于对比)
func (s *LeaderboardService) GetTopN(n int64) ([]RankInfo, error) {
	results, err := s.rdb.ZRevRangeWithScores(s.ctx, leaderboardKey, 0, n-1).Result()
	if err != nil {
		return nil, err
	}
	rankings := make([]RankInfo, len(results))
	for i, member := range results {
		rankings[i] = RankInfo{
			PlayerID: member.Member.(string),
			Score:    int64(member.Score / scoreMultiplier),
			Rank:     int64(i + 1), // 标准排名
		}
	}
	return rankings, nil
}

// =================================================================
// 选做题：新增的密集排名方法
// =================================================================

// GetPlayerRankDense 获取玩家的密集排名
func (s *LeaderboardService) GetPlayerRankDense(playerID string) (*RankInfo, error) {
	// 1. 获取玩家自己的分数
	combinedScore, err := s.rdb.ZScore(s.ctx, leaderboardKey, playerID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("player %s not found", playerID)
		}
		return nil, err
	}
	score := int64(combinedScore / scoreMultiplier)

	// 2. 计算分数比该玩家【严格】高的玩家数量
	// ZRevCount 返回 [min, max] 范围内的成员数。我们查询 (+inf, combinedScore) 开区间
	// 需要将 combinedScore 转换为字符串，并在前面加上 '(' 表示开区间
	exclusiveScoreStr := fmt.Sprintf("(%f", combinedScore)
	higherScoreCount, err := s.rdb.ZCount(s.ctx, leaderboardKey, "+inf", exclusiveScoreStr).Result()
	if err != nil {
		return nil, err
	}

	// 3. 密集排名 = 分数比他高的人数 + 1
	denseRank := higherScoreCount + 1

	return &RankInfo{
		PlayerID: playerID,
		Score:    score,
		Rank:     denseRank,
	}, nil
}

// GetTopNDense 获取前 N 名玩家（密集排名）
func (s *LeaderboardService) GetTopNDense(limit int64) ([]RankInfo, error) {
	// 为了获取前 N 个排名，我们可能需要获取超过 N 个玩家
	// 这里做一个简化，我们先获取一个较多的数量，例如前 100 名
	results, err := s.rdb.ZRevRangeWithScores(s.ctx, leaderboardKey, 0, 99).Result()
	if err != nil {
		return nil, err
	}

	rankings := make([]RankInfo, 0)
	if len(results) == 0 {
		return rankings, nil
	}

	currentRank := int64(1)
	// 添加第一名
	firstPlayerScore := int64(results[0].Score / scoreMultiplier)
	rankings = append(rankings, RankInfo{
		PlayerID: results[0].Member.(string),
		Score:    firstPlayerScore,
		Rank:     currentRank,
	})

	for i := 1; i < len(results); i++ {
		currentScore := int64(results[i].Score / scoreMultiplier)
		prevScore := int64(results[i-1].Score / scoreMultiplier)

		// 如果分数与上一个不同，排名+1
		if currentScore < prevScore {
			currentRank++
		}

		// 如果我们只需要前 limit 个排名
		if limit > 0 && currentRank > limit {
			break
		}

		rankings = append(rankings, RankInfo{
			PlayerID: results[i].Member.(string),
			Score:    currentScore,
			Rank:     currentRank,
		})
	}
	return rankings, nil
}

// =================================================================
// main 函数 - 用于演示和测试
// =================================================================
func main() {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		fmt.Printf("无法连接到 Redis: %v\n", err)
		return
	}
	service := NewLeaderboardService(rdb)

	// 准备测试数据并清理环境 ---
	fmt.Println("--- 准备测试数据 (用于密集排名测试) ---")
	rdb.Del(context.Background(), leaderboardKey)

	players := []struct {
		ID        string
		Score     int64
		Timestamp int64
	}{
		{"playerA", 100, time.Now().Unix() - 100}, // 100分, 时间早 -> 排名并列第2
		{"playerB", 100, time.Now().Unix() - 50},  // 100分, 时间晚 -> 排名并列第2
		{"playerC", 95, time.Now().Unix() - 80},   // 95分 -> 排名第3
		{"playerD", 120, time.Now().Unix() - 60},  // 120分 -> 排名第1
		{"playerE", 90, time.Now().Unix() - 40},   // 90分 -> 排名第4
		{"playerF", 95, time.Now().Unix() - 20},   // 95分 -> 排名并列第3
	}

	for _, p := range players {
		service.UpdateScore(p.ID, p.Score, p.Timestamp)
	}
	fmt.Println("测试数据写入完成。")
	fmt.Println("========================================")

	// 执行测试并打印结果 ---

	fmt.Println("\n--- 对比：标准排名 (Top 6) ---")
	top6, _ := service.GetTopN(6)
	fmt.Println("名次 | 玩家ID   | 分数")
	fmt.Println("-----|----------|------")
	for _, p := range top6 {
		fmt.Printf("%-4d | %-8s | %d\n", p.Rank, p.PlayerID, p.Score)
	}
	fmt.Println("========================================")

	// 测试 GetTopNDense
	fmt.Println("\n--- 测试：密集排名 (GetTopNDense) ---")
	topDense, err := service.GetTopNDense(0) // limit=0 表示获取所有
	if err != nil {
		fmt.Printf("获取密集排名失败: %v\n", err)
	} else {
		fmt.Println("名次 | 玩家ID   | 分数")
		fmt.Println("-----|----------|------")
		for _, p := range topDense {
			fmt.Printf("%-4d | %-8s | %d\n", p.Rank, p.PlayerID, p.Score)
		}
	}
	fmt.Println("========================================")

	// 测试 GetPlayerRankDense
	fmt.Println("\n--- 测试：查询单个玩家的密集排名 (GetPlayerRankDense) ---")
	testPlayersForDenseRank := []string{"playerA", "playerB", "playerC", "playerF"}
	for _, playerID := range testPlayersForDenseRank {
		rankInfo, err := service.GetPlayerRankDense(playerID)
		if err != nil {
			fmt.Printf("查询玩家 %s 密集排名失败: %v\n", playerID, err)
		} else {
			fmt.Printf("玩家 %s 的信息: 密集排名=%d, 分数=%d\n", playerID, rankInfo.Rank, rankInfo.Score)
		}
	}
	fmt.Println("========================================")
}
