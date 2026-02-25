# swarm-autoscaler

> CPU 사용률 기반으로 Docker Swarm 서비스 레플리카를 자동으로 조절하는 오토스케일러

<br>

## Overview

Docker Swarm은 Kubernetes와 달리 기본 오토스케일러가 없다.
이 프로젝트는 각 서비스의 CPU 사용률을 모니터링하고, 설정한 목표치에 따라 레플리카 수를 자동으로 늘리거나 줄인다.

```
[Docker Swarm Services]
        ↓  (20s interval)
  [swarm-autoscaler]
    ├─ CPU 수집 (ContainerStats API)
    ├─ EMA 스무딩 (α=0.3)
    ├─ Hysteresis 판단 (±10% / -40%)
    └─ Replica 조정 (max +2 / -1 per cycle)
        ↓
[docker service update]
```

<br>

## How It Works

### 스케일링 알고리즘

```
target CPU = 50%

upper = target × 1.10 = 55%   →  EMA > 55%  이면 스케일 업
lower = target × 0.60 = 30%   →  EMA < 30%  이면 스케일 다운
dead zone = 30% ~ 55%         →  변화 없음
```

- **EMA (Exponential Moving Average)**: 순간적인 CPU 스파이크에 반응하지 않도록 스무딩
- **Hysteresis**: 임계값 근처에서 스케일 업/다운이 반복되는 플래핑(flapping) 방지
- **Step Limit**: 한 사이클에 최대 +2 / -1 레플리카만 변경해 오버슈팅 방지
- **Cooldown**: 스케일링 후 60초 대기해 연속 조정 방지

<br>

## Quick Start

### Dry-run (Docker 없이 테스트)

```bash
AUTOSCALER_DRY_RUN=true go run cmd/autoscaler/main.go
```

mock 서비스와 CPU 시계열 데이터(`testdata/`)를 사용해 실제 스케일링 없이 동작을 시뮬레이션한다.

### Docker로 실행

```bash
docker build -t swarm-autoscaler .

docker run -d \
  -v /var/run/docker.sock:/var/run/docker.sock \
  swarm-autoscaler
```

<br>

## Service Configuration

서비스 레이블로 오토스케일러를 제어한다.

```yaml
services:
  web:
    image: nginx
    deploy:
      replicas: 3
      labels:
        autoscaler.enable: "true"
        autoscaler.target.cpu: "50"   # 목표 CPU 사용률 (%)
        autoscaler.min: "2"           # 최소 레플리카 수
        autoscaler.max: "10"          # 최대 레플리카 수
```

| 레이블 | 설명 | 예시 |
|--------|------|------|
| `autoscaler.enable` | 오토스케일링 활성화 | `"true"` |
| `autoscaler.target.cpu` | 목표 CPU 사용률 (%) | `"50"` |
| `autoscaler.min` | 최소 레플리카 수 | `"2"` |
| `autoscaler.max` | 최대 레플리카 수 | `"10"` |

<br>

## Environment Variables

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `AUTOSCALER_DRY_RUN` | `false` | dry-run 모드 활성화 |
| `AUTOSCALER_DRY_RUN_FILE` | `testdata/docker-services.json` | mock 서비스 데이터 경로 |
| `AUTOSCALER_DRY_RUN_CPU_FILE` | `testdata/docker-cpu.json` | mock CPU 시계열 데이터 경로 |

<br>

## Project Structure

```
swarm-autoscaler/
├── cmd/autoscaler/main.go          # 진입점 — 20초 간격 Reconcile 루프
├── internal/
│   ├── config/config.go            # 환경변수 설정
│   ├── docker/
│   │   ├── client.go               # Docker 클라이언트 초기화
│   │   ├── service.go              # Swarm 서비스 목록 조회
│   │   └── stats.go                # 컨테이너 CPU 통계 수집
│   └── scaler/
│       ├── algorithm.go            # EMA + 스케일링 알고리즘
│       ├── controller.go           # Reconcile 루프 (핵심 로직)
│       └── state.go                # 서비스별 상태 관리
├── testdata/
│   ├── docker-services.json        # dry-run용 mock 서비스
│   └── docker-cpu.json             # dry-run용 CPU 시계열
└── Dockerfile                      # Multi-stage build (distroless)
```

<br>

## Roadmap

- [x] Phase 1 — CPU 기반 오토스케일링
- [ ] Phase 2 — Memory 기반 오토스케일링
- [ ] Phase 3 — 커스텀 메트릭 (Prometheus) 기반 오토스케일링
- [ ] Prometheus `/metrics` 엔드포인트 (관찰 가능성)
- [ ] 멀티노드 Swarm 지원

<br>

## Current Limitations

- 노드 전체 가용 CPU를 확인하지 않고 스케일 업 결정
- Worker 노드에 분산된 컨테이너의 CPU를 Manager에서 직접 수집 불가
- Mac Docker Desktop은 VM이 1개뿐이므로 멀티노드 환경 구성 불가

멀티노드 테스트는 EC2/GCP VM 또는 Multipass/Vagrant 클러스터가 필요하다.
