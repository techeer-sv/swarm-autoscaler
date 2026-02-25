# swarm-autoscaler 정리

## 목차
1. [프로젝트 구조 파악](#1-프로젝트-구조-파악)
2. [EMA와 스케일 다운 시뮬레이션](#2-ema와-스케일-다운-시뮬레이션)
3. [싱글노드 한계](#3-싱글노드-한계)
4. [스케일 업 step limit](#4-스케일-업-step-limit)
5. [왜 +2 고정인가](#5-왜-2-고정인가)
6. [기여 방향](#6-기여-방향)
7. [로드맵](#7-로드맵)
8. [이슈 리스트](#8-이슈-리스트)

---

## 1. 프로젝트 구조 파악

### 디렉토리 구조

```
swarm-autoscaler/
├── cmd/autoscaler/main.go          # 진입점 (20초 간격 루프)
├── internal/
│   ├── config/config.go            # 환경변수 설정 (DryRun 등)
│   ├── docker/
│   │   ├── client.go               # Docker 클라이언트 초기화
│   │   ├── service.go              # 서비스 목록 조회
│   │   └── stats.go                # 컨테이너 CPU 측정
│   └── scaler/
│       ├── algorithm.go            # EMA + 스케일링 알고리즘
│       ├── controller.go           # Reconcile 루프 (핵심)
│       └── state.go                # 서비스별 상태 관리
├── testdata/
│   ├── docker-services.json        # 목 서비스 데이터
│   └── docker-cpu.json             # 목 CPU 시계열 데이터
└── Dockerfile                      # Multi-stage, distroless
```

### 레이블 기반 설정

Docker Swarm 서비스에 레이블을 붙여서 오토스케일러를 제어한다.

```
autoscaler.enable=true        # 이 서비스를 스케일링 대상으로 지정
autoscaler.target.cpu=50      # 목표 CPU 사용률 (%)
autoscaler.min=2              # 최소 레플리카 수
autoscaler.max=10             # 최대 레플리카 수
```

### 동작 흐름 (20초마다 반복)

```
Docker Swarm 서비스 목록 조회
    → CPU 사용량 수집 (컨테이너별)
    → EMA(지수이동평균)로 노이즈 제거
    → 목표치와 비교 (Hysteresis 적용)
    → 레플리카 수 조정 (최대 +2 / -1)
    → 60초 쿨다운
```

### 인프라 핵심 패턴

| 패턴 | 파일 | 개념 |
|------|------|------|
| Dry-run 모드 | `config/config.go` | 실제 반영 없이 테스트 |
| 쿨다운/Hysteresis | `scaler/algorithm.go` | 과도한 스케일링 방지 |
| Reconciliation Loop | `scaler/controller.go` | K8s controller 패턴과 동일 |
| 레이블 기반 설정 | `scaler/controller.go` | 선언적 인프라 관리 |

### Dockerfile 특징

- **Multi-stage build**: 빌드용(`golang:alpine`) / 실행용(`distroless`) 이미지 분리
- **distroless**: 쉘, OS 유틸리티 없음 → 최소 공격 표면
- **CGO_ENABLED=0**: 정적 바이너리 컴파일 (외부 의존성 없음)

---

## 2. EMA와 스케일 다운 시뮬레이션

### 설정값 (web 서비스 기준)

```
target = 50%
upper  = 50 * 1.10 = 55%    ← 이 이상이면 스케일 업
lower  = 50 * 0.60 = 30%    ← 이 미만이면 스케일 다운
dead zone = 30 ~ 55%        ← 이 구간은 아무것도 안 함
```

> **주의**: lower는 `/1.1 = 45.5%`가 아니라 `* 0.60 = 30%`
> 스케일 다운 조건이 의도적으로 보수적으로 설계되어 있음

### EMA 공식

```
new_EMA = 0.3 × 현재CPU + 0.7 × 이전EMA
```

alpha = 0.3이므로 현재값 30%, 이전 이력 70% 반영 → 급격한 변화에 둔감

### CPU 90% → 20%로 급락 시 EMA 추적

EMA 초기값 = 90 (고CPU 상태 지속 후), 실제 CPU = 20%로 급락

| 사이클 | 실제 CPU | EMA 계산 | EMA | 판정 |
|--------|---------|---------|-----|------|
| 0 (기준) | 90 | - | **90.0** | 스케일업 중 |
| 1 | 20 | 0.3×20 + 0.7×90 | **69.0** | dead zone 못 벗어남 |
| 2 | 20 | 0.3×20 + 0.7×69 | **54.3** | dead zone 진입 |
| 3 | 20 | 0.3×20 + 0.7×54.3 | **44.0** | dead zone |
| 4 | 20 | 0.3×20 + 0.7×44.0 | **36.8** | dead zone |
| 5 | 20 | 0.3×20 + 0.7×36.8 | **31.8** | dead zone |
| 6 | 20 | 0.3×20 + 0.7×31.8 | **28.2** | **스케일 다운 발동!** |

**결론: 실제 CPU가 20%로 떨어져도 6사이클(2분)이 지나야 스케일 다운 시작**

### 스케일 다운이 가능한 조건

EMA는 장기적으로 실제 CPU값으로 수렴한다.

```
실제 CPU > 30%  → EMA 수렴값 ≥ 30 → 스케일 다운 영구 불가
실제 CPU = 30%  → EMA 수렴값 = 30 → 경계선 (발동 안 됨)
실제 CPU < 30%  → EMA 수렴값 < 30 → 결국 스케일 다운 발동
```

**스케일 다운을 위해 실제 CPU가 반드시 30% 미만으로 유지되어야 한다.**

### 스케일 다운 시 레플리카 계산

EMA < 30이 되면: `desired = 현재레플리카 × (EMA / target)`
단, 한 번에 최대 `-1`씩만 감소

```
현재 6개, EMA = 25
desired = 6 × (25/50) = 3개
but 한 번에 -1만 → 6→5→4→3 (3사이클 + 쿨다운 60초씩)
```

### hysteresis 설계 비교

| 방식 | lower 값 | 스케일 다운 조건 |
|------|---------|----------------|
| `target / 1.1` | **45.5%** | EMA < 45.5% (빈번함) |
| `target * 0.60` (현재 코드) | **30%** | EMA < 30% (보수적) |

현재 코드는 의도적으로 스케일 다운을 어렵게 설정 → "플래핑(flapping)" 방지

---

## 3. 싱글노드 한계

### 한계 1: 노드 여유 CPU 미계산

`controller.go:100-104`에 명시된 TODO:

```go
// TODO:
// 1. Fetch task containers
// 2. Compute average CPU across containers
// For now, mock or use dry-run CPU
avgCPU := 50.0   // ← 하드코딩된 값, 프로덕션에서 무의미
```

현재 스케일링 판단:
```
서비스 CPU 90% → 레플리카 추가!
```

빠진 판단:
```
서비스 CPU 90% → 노드 전체 CPU 여유분 확인 → 여유 있으면 → 레플리카 추가
```

노드 CPU가 100%인데 레플리카를 추가하면 컨테이너가 생성되지만 CPU를 못 받아 오히려 더 느려진다.

### 한계 2: 멀티노드에서 컨테이너 CPU 수집 불가

`stats.go`의 `GetContainerCPU()`는 `ContainerStats` API를 사용한다.
이 API는 **해당 컨테이너가 실행 중인 노드에서만** 응답한다.

```
싱글노드:
  Manager/Worker: [컨테이너A] [컨테이너B] [컨테이너C]
  → 같은 노드, 같은 API로 전부 조회 가능 ✅

멀티노드:
  Node1 (Manager): [컨테이너A]
  Node2 (Worker):  [컨테이너B]   ← Manager에서 직접 API 호출 불가 ❌
  Node3 (Worker):  [컨테이너C]   ← Manager에서 직접 API 호출 불가 ❌
```

### Mac 환경의 근본 제약

```
Mac Docker Desktop
    └── VM 1개
         └── Docker Engine (Manager + Worker 동시)
              ├── 노드 추가 불가 (VM이 1개뿐)
              └── 멀티노드 시뮬레이션 불가
```

**이 프로젝트가 `feat/single-node` 브랜치인 이유.**
실제 멀티노드 테스트는 EC2/GCP VM 3대 또는 Multipass/Vagrant 로컬 VM 클러스터가 필요하다.

### 멀티노드에서 올바른 CPU 수집 방법 (현재 미구현)

| 방법 | 설명 |
|------|------|
| Prometheus + cAdvisor | 각 Worker Node에 cAdvisor 설치 → Prometheus 수집 → 오토스케일러 쿼리 |
| Docker Swarm Task API | ServiceTasks 조회 → NodeID 확인 → 해당 Node API 호출 (접근 권한 문제 있음) |
| Overlay Network + Sidecar | 각 컨테이너 옆에 metrics sidecar 배포 → 내부 네트워크 수집 |

---

## 4. 스케일 업 step limit

`algorithm.go:39-50`

```go
maxUpStep   := uint64(2)
maxDownStep := uint64(1)

if desired > currentReplicas {
    if desired-currentReplicas > maxUpStep {
        desired = currentReplicas + maxUpStep  // 최대 +2
    }
}
```

**한 사이클(20초)에 최대 +2개만 증가**, 스케일 다운은 -1개만 감소.

### 스케일 업/다운 비대칭

| 방향 | 한 사이클 최대 변화 | 이유 |
|------|----------------|------|
| 스케일 업 | **+2** | 빠른 대응 필요 |
| 스케일 다운 | **-1** | 갑작스러운 제거 시 트래픽 처리 공백 방지 |

---

## 5. 왜 +2 고정인가

### 한번에 많이 늘리면 생기는 문제

**문제 1: 오버슈팅(Overshoot)**
```
CPU 90% → 10개 한번에 추가 → 컨테이너 뜨는데 5~10초 걸림
→ 그 사이 CPU 측정값은 여전히 높음
→ 다음 사이클에 또 10개 추가 명령
→ 레플리카 폭발
```

**문제 2: 노드 리소스 한번에 고갈**
```
노드 CPU 여유: 40%
레플리카 10개 한번에 추가
→ 각 컨테이너가 CPU 경쟁 → 전부 느려짐
→ CPU 더 올라감 → 또 스케일 업 트리거
```

**문제 3: 쿨다운이 의미 없어짐**
1사이클에 너무 많이 늘려버리면 쿨다운 전에 이미 과잉 상태가 됨.

### +2 고정의 한계

```
현재 레플리카 1개, CPU 200% → 필요한 건 4개
→ 1→3→5 (2사이클 40초 + 쿨다운 60초씩)
→ 실제로는 2분 넘게 걸림
```

### 현재 코드 vs Kubernetes HPA

| | 이 프로젝트 (+2 고정) | K8s HPA (2배 비율) |
|---|---|---|
| 2개 → 필요 8개 | 2→4→6→8 (3사이클) | 2→4→8 (2사이클) |
| 10개 → 필요 20개 | 10→12→14→... (5사이클) | 10→20 (1사이클) |
| 특징 | 단순하고 보수적 | 상황에 따라 유연 |

K8s HPA는 고정 숫자가 아닌 **비율**로 제한한다: 한 번에 최대 2배까지.

현재 코드는 싱글노드 + 학습/실험용이라 단순하게 고정값으로 설계되어 있다.

---

## 6. 기여 방향

### A. 로직 보완 (코드 작성)

| 작업 | 난이도 | 내용 |
|------|--------|------|
| 컨테이너 목록 조회 | 낮음 | 서비스에 속한 Task(컨테이너) 목록 가져오기 |
| 평균 CPU 계산 | 낮음 | `GetContainerCPU` 여러 번 호출 후 평균 |
| 노드 여유 CPU 체크 | 중간 | 스케일 업 전 노드 전체 CPU 확인 |
| README 보완 | 낮음 | 실행 방법, 레이블 설명 문서화 |

### B. 이슈 발굴 (테스트 + 분석)

| 이슈 | 내용 |
|------|------|
| 쿨다운 중 CPU 급등 | 60초 쿨다운 동안 CPU가 계속 오르면 아무것도 안 함 |
| `min=0` 설정 시 | 서비스가 0개로 내려갈 수 있음 |
| 레이블 누락 시 | `autoscaler.target.cpu` 없으면 `targetCPU=0` → 현재 유지 (조용히 실패) |
| 컨테이너 시작 지연 | 스케일 업 후 컨테이너 뜨기 전에 다음 사이클이 돌면? |

### 추천 시작점 (Go 모를 때)

```
1. dry-run으로 직접 실행해보면서 로그 관찰
2. 엣지케이스(min=0, 레이블 오타 등) 직접 재현
3. GitHub Issue로 정리
4. 재현 방법 + 예상 동작 + 실제 동작 문서화
```

---

## 7. 로드맵

### Phase 1: CPU 오토스케일러 ✅ (완료)

```
측정:  ContainerStats → CPUUsage delta
지표:  % (0~100 * numCPU)
특징:  순간적으로 튀었다 내려옴 → EMA 스무딩 필요
```

### Phase 2: Memory 오토스케일러

**CPU와 다른 핵심 차이점**

```
CPU    → 쓰다 안 쓰면 즉시 반환 (elastic)
Memory → 한번 할당하면 프로세스가 해제 안 하면 유지 (sticky)
```

**측정 방법** (같은 Stats API, 다른 필드)

```go
v.MemoryStats.Usage   // 현재 사용량 (bytes)
v.MemoryStats.Limit   // 컨테이너 메모리 한도 (bytes)

memPercent := float64(v.MemoryStats.Usage) / float64(v.MemoryStats.Limit) * 100
```

**CPU vs Memory 스케일링 비교**

| | CPU | Memory |
|--|-----|--------|
| 높으면 | 스케일 업 | 스케일 업 (OOM 위험) |
| 낮으면 | 스케일 다운 | 신중해야 함 |
| EMA 필요? | 필수 | 덜 필요 (메모리는 잘 안 튐) |
| 위험 상황 | 느려짐 | **OOM Kill (강제 종료)** |

**주의**: 메모리 리크가 있는 서비스는 레플리카를 늘려도 각 컨테이너 메모리가 계속 증가 → 스케일 업이 근본 해결이 아니므로 알람/경고 로직이 중요.

### Phase 3: 커스텀 메트릭 오토스케일러

**아키텍처 변화**

```
Phase 1~2: Docker Stats API (내장)
Phase 3:   외부 메트릭 소스 필요

[서비스] → /metrics 노출
               ↓
          [Prometheus]  ← 수집
               ↓
          [오토스케일러] ← PromQL 쿼리
               ↓
          [Docker Swarm] ← 스케일 명령
```

**커스텀 메트릭 예시**
- 큐 대기 메시지 수 (RabbitMQ, Kafka)
- HTTP 요청 레이턴시 (p99)
- DB 커넥션 풀 사용률
- 초당 요청 수 (RPS)

**레이블 확장 필요**

```
# 현재 (Phase 1~2)
autoscaler.target.cpu=50

# Phase 3
autoscaler.metric.source=prometheus
autoscaler.metric.query=queue_depth{service="worker"}
autoscaler.metric.target=100
```

### 전체 로드맵 요약

```
Phase 1  CPU 오토스케일러       ✅ 완료
Phase 2  Memory 오토스케일러    → stats.go 확장 + 알고리즘 분리
Phase 3  커스텀 메트릭          → Prometheus 연동 + 메트릭 소스 추상화
```

> **설계 포인트**: Phase 2에서 CPU 로직과 Memory 로직을 어떻게 분리하느냐가 핵심.
> 지금 구조에 끼워넣으면 Phase 3 추가 시 복잡도가 폭발한다.
> 인터페이스/추상화 설계를 이슈로 먼저 정리하는 것을 권장.

---

## 8. 이슈 리스트

### Issue 1: 실제 컨테이너 CPU 수집 미구현

- **파일**: `internal/scaler/controller.go:100-104`
- **현상**: `avgCPU := 50.0` 하드코딩. 실제 컨테이너 CPU를 읽지 않음
- **필요 작업**:
  1. 서비스에 속한 Task(컨테이너) 목록 조회
  2. 각 컨테이너에 `GetContainerCPU()` 호출
  3. 평균값 계산 후 EMA에 입력
- **관련 TODO**: 코드 내 주석으로 명시되어 있음

---

### Issue 2: 노드 여유 CPU 확인 없이 스케일 업

- **현상**: 노드 전체 CPU가 100%여도 레플리카 추가 명령이 나감
- **결과**: 컨테이너가 생성되지만 CPU를 못 받아 오히려 성능 저하
- **필요 작업**: 스케일 업 결정 전 노드 가용 리소스 확인 로직 추가
- **관련**: 싱글노드 한계와 직결됨

---

### Issue 3: 멀티노드 환경에서 Worker 컨테이너 CPU 수집 불가

- **현상**: `ContainerStats` API는 컨테이너가 있는 노드에서만 응답
- **결과**: Manager가 Worker 노드의 컨테이너 stats를 직접 조회할 수 없음
- **재현 조건**: 멀티노드 Swarm 클러스터 (Mac 단독으로는 재현 불가)
- **해결 방향**: Prometheus + cAdvisor 도입 또는 Swarm Task API + 노드별 엔드포인트

---

### Issue 4: 쿨다운 중 CPU 급등 시 대응 없음

- **현상**: 스케일 업 후 60초 쿨다운 중 CPU가 계속 오르면 아무 조치도 하지 않음
- **결과**: 쿨다운이 오히려 장애 대응을 막는 상황 발생 가능
- **재현 방법**: `testdata/docker-cpu.json`에서 CPU가 지속적으로 오르는 시계열 데이터로 테스트
- **해결 방향**: 임계값 초과 시 쿨다운 무시하는 긴급 스케일 업 로직 검토

---

### Issue 5: `min=0` 설정 시 서비스 레플리카 0개 가능

- **현상**: `autoscaler.min=0` 레이블 설정 시 스케일 다운이 0까지 내려갈 수 있음
- **결과**: 서비스 완전 중단
- **재현 방법**: `testdata/docker-services.json`에서 `min` 값을 0으로 변경 후 dry-run
- **해결 방향**: min 값 최솟값을 1로 강제하거나 0 입력 시 경고 로그 출력

---

### Issue 6: 레이블 누락/오타 시 조용히 실패

- **현상**: `autoscaler.target.cpu` 레이블이 없으면 `targetCPU=0` 으로 파싱됨
- **결과**: `ComputeDesiredReplicas`에서 `target <= 0` 조건에 걸려 현재 레플리카 수 그대로 유지. 에러 없음
- **문제**: 설정 오류인지 정상 동작인지 운영자가 알 수 없음
- **해결 방향**: 레이블 파싱 실패 시 명시적 경고 로그 출력

---

### Issue 7: 메트릭 엔드포인트 부재 (관찰 가능성 부족)

- **현상**: 오토스케일러 동작 상태를 로그로만 확인 가능
- **문제**: 로그는 추세 파악, 알람, 대시보드 불가. 실시간으로 "잘 돌아가고 있는지" 판단 어려움
- **필요한 메트릭**:

  | 메트릭 이름 | 종류 | 설명 |
  |------------|------|------|
  | `autoscaler_replicas_current{service}` | Gauge | 현재 레플리카 수 |
  | `autoscaler_replicas_desired{service}` | Gauge | 목표 레플리카 수 |
  | `autoscaler_cpu_ema{service}` | Gauge | 현재 EMA 값 |
  | `autoscaler_cpu_actual{service}` | Gauge | 실제 CPU % |
  | `autoscaler_cooldown_active{service}` | Gauge | 쿨다운 중 여부 (0/1) |
  | `autoscaler_scale_up_total{service}` | Counter | 스케일 업 누적 횟수 |
  | `autoscaler_scale_down_total{service}` | Counter | 스케일 다운 누적 횟수 |
  | `autoscaler_reconcile_errors_total` | Counter | Reconcile 에러 횟수 |
  | `autoscaler_last_reconcile_timestamp` | Gauge | 마지막 실행 시각 (생존 확인) |

- **해결 방향**: `prometheus/client_golang` 라이브러리로 `/metrics` HTTP 엔드포인트 추가
- **우선순위**: Phase 2(Memory) 개발 전에 먼저 구현해야 메모리 메트릭도 검증 가능
