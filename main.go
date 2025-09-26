package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	leaderboardKey = "game:leaderboard:main_test" // 使用一个独立的key，避免污染数据
	// 用于组合 score 和 timestamp，假设时间戳是秒级的
	// 分数乘以一个大数是为了让分数在组合后的 score 中占据主导地位
	scoreMultiplier      = 1e12
	maxTimestampReversed = 1e12
)

// RankInfo 存储玩家的排名信息
type RankInfo struct {
	PlayerID string `json:"playerId"`
	Score    int64  `json:"score"`
	Rank     int64  `json:"rank"`
}

// LeaderboardService 是排行榜系统的核心服务
type LeaderboardService struct {
	rdb *redis.Client
	ctx context.Context
}

// NewLeaderboardService 创建一个新的排行榜服务实例
func NewLeaderboardService(rdb *redis.Client) *LeaderboardService {
	return &LeaderboardService{
		rdb: rdb,
		ctx: context.Background(),
	}
}

// UpdateScore 更新玩家积分
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

// GetPlayerRank 查询玩家当前排名
func (s *LeaderboardService) GetPlayerRank(playerID string) (*RankInfo, error) {
	rank, err := s.rdb.ZRevRank(s.ctx, leaderboardKey, playerID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("player %s not found in leaderboard", playerID)
		}
		return nil, err
	}

	combinedScore, err := s.rdb.ZScore(s.ctx, leaderboardKey, playerID).Result()
	if err != nil {
		return nil, err
	}
	score := int64(combinedScore / scoreMultiplier)

	return &RankInfo{
		PlayerID: playerID,
		Score:    score,
		Rank:     rank + 1, // 转换为 1-based 排名
	}, nil
}

// GetTopN 获取前 N 名玩家
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
			Rank:     int64(i + 1),
		}
	}
	return rankings, nil
}

// GetPlayerRankRange 查询自己名次前后共 N 名玩家
func (s *LeaderboardService) GetPlayerRankRange(playerID string, nRange int64) ([]RankInfo, error) {
	playerRankInfo, err := s.GetPlayerRank(playerID)
	if err != nil {
		return nil, err
	}
	playerRank := playerRankInfo.Rank

	startRank := playerRank - (nRange / 2)
	if startRank < 1 {
		startRank = 1
	}
	endRank := startRank + nRange - 1

	results, err := s.rdb.ZRevRangeWithScores(s.ctx, leaderboardKey, startRank-1, endRank-1).Result()
	if err != nil {
		return nil, err
	}

	rankings := make([]RankInfo, len(results))
	for i, member := range results {
		rankings[i] = RankInfo{
			PlayerID: member.Member.(string),
			Score:    int64(member.Score / scoreMultiplier),
			Rank:     startRank + int64(i),
		}
	}
	return rankings, nil
}

// =================================================================
// main 函数 - 用于演示和测试
// =================================================================
func main() {
	//  初始化 ---
	// 假设本地 Redis 在默认端口 6379 上运行
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// 检查 Redis 连接
	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		fmt.Printf("无法连接到 Redis: %v\n", err)
		fmt.Println("请确保本地 6379 端口的 Redis 服务正在运行。")
		return
	}

	service := NewLeaderboardService(rdb)

	// 准备测试数据并清理环境 ---
	fmt.Println("--- 准备测试数据 ---")
	// 清理旧数据，保证测试环境干净
	rdb.Del(context.Background(), leaderboardKey)

	// 准备玩家数据
	players := []struct {
		ID        string
		Score     int64
		Timestamp int64
	}{
		{"playerA", 100, time.Now().Unix() - 100}, // 100分, 时间早
		{"playerB", 100, time.Now().Unix() - 50},  // 100分, 时间晚
		{"playerC", 95, time.Now().Unix() - 80},
		{"playerD", 120, time.Now().Unix() - 60}, // 最高分
		{"playerE", 90, time.Now().Unix() - 40},
		{"playerF", 89, time.Now().Unix() - 20},
		{"playerG", 105, time.Now().Unix() - 30},
	}

	// 写入初始分数
	for _, p := range players {
		err := service.UpdateScore(p.ID, p.Score, p.Timestamp)
		if err != nil {
			fmt.Printf("为玩家 %s 更新分数失败: %v\n", p.ID, err)
			return
		}
	}
	fmt.Println("测试数据写入完成。")
	fmt.Println("========================================")

	// 执行测试并打印结果 ---

	// 测试 GetTopN
	fmt.Println("\n--- 测试 GetTopN(5) ---")
	top5, err := service.GetTopN(5)
	if err != nil {
		fmt.Printf("获取 Top 5 失败: %v\n", err)
	} else {
		fmt.Println("排行榜前 5 名:")
		for _, p := range top5 {
			fmt.Printf("排名: %d, 玩家: %s, 分数: %d\n", p.Rank, p.PlayerID, p.Score)
		}
	}
	fmt.Println("========================================")

	// 测试 GetPlayerRank
	fmt.Println("\n--- 测试 GetPlayerRank ---")
	testPlayersForRank := []string{"playerA", "playerD", "playerF"}
	for _, playerID := range testPlayersForRank {
		rankInfo, err := service.GetPlayerRank(playerID)
		if err != nil {
			fmt.Printf("查询玩家 %s 排名失败: %v\n", playerID, err)
		} else {
			fmt.Printf("玩家 %s 的信息: 排名=%d, 分数=%d\n", playerID, rankInfo.Rank, rankInfo.Score)
		}
	}
	fmt.Println("========================================")

	// 测试 UpdateScore
	fmt.Println("\n--- 测试 UpdateScore (playerF 增加 20分) ---")
	fmt.Println("playerF 初始分数 89...")
	err = service.UpdateScore("playerF", 20, time.Now().Unix())
	if err != nil {
		fmt.Printf("为 playerF 更新分数失败: %v\n", err)
	} else {
		rankInfo, _ := service.GetPlayerRank("playerF")
		fmt.Printf("玩家 playerF 的新信息: 排名=%d, 分数=%d\n", rankInfo.Rank, rankInfo.Score)
	}
	fmt.Println("========================================")

	// 测试 GetPlayerRankRange
	fmt.Println("\n--- 测试 GetPlayerRankRange ---")
	targetPlayer := "playerG"
	var nRange int64 = 4
	fmt.Printf("查询玩家 %s 周边共 %d 名的排名...\n", targetPlayer, nRange)
	rangeData, err := service.GetPlayerRankRange(targetPlayer, nRange)
	if err != nil {
		fmt.Printf("查询玩家 %s 周边排名失败: %v\n", targetPlayer, err)
	} else {
		fmt.Printf("玩家 %s 周边排名结果:\n", targetPlayer)
		for _, p := range rangeData {
			fmt.Printf("排名: %d, 玩家: %s, 分数: %d\n", p.Rank, p.PlayerID, p.Score)
		}
	}
	fmt.Println("========================================")

}
