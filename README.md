## 介绍
1. ./main.go 标准实现.
2. ./service/main.go 选做题.

## 运行
### 1. redis
```bash
 docker run --name my-redis -p 6379:6379 -d redis:7
```

### 2. go tidy
```bash
 go mod tidy
```

### 3. go run 
```bash
 go run ./main.go
```
