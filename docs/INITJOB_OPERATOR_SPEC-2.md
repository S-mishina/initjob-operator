# InitJob Operator — Specification

## 概要 / Overview

InitJob Operator は Kubernetes 上で **初期化処理（init 処理）を宣言的に管理するための Custom Resource（InitJob）** を提供するオペレーターです。

- InitJob CR を作成すると、その `spec.jobTemplate` を元に **Kubernetes Job が1回だけ実行** される
- Job が完了して削除されても、InitJob CR は削除されず **実行結果の記録として残る**
- **InitJob に対して再度 `kubectl apply` などで変更が入り、その内容が前回実行した Job と「差分あり」と判定されたときのみ、Job を再実行する**
- 差分がない場合は、Job が存在していなくても新しい Job は作られない
- Reconcile は冪等であり、意図しない Job の増殖は発生しない

> イメージ:
> - InitJob = 「init 処理の定義と実行履歴を持つリソース」
> - Job = 「その時点の InitJob の内容で 1 回だけ実行される実行体」

---

## ユースケース

- データベースの初期テーブル作成
- 初期データ投入
- 外部サービスへの初回プロビジョニング
- デプロイ前後の一度きりの検証処理（preflight / post-deploy init）
- マイクロサービスの初回同期処理（Config/Secret sync など）

---

## CRD 定義

### API 情報

| 項目 | 値 |
|------|------|
| Group | `batch.init.sre.example.com` |
| Version | `v1alpha1` |
| Kind | `InitJob` |
| Scope | Namespaced |

---

## InitJob CR — Spec

```yaml
apiVersion: batch.init.sre.example.com/v1alpha1
kind: InitJob
metadata:
  name: sample-init
spec:
  jobTemplate:
    metadata:
      labels:
        app: sample-init
    spec:
      backoffLimit: 3
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: init
              image: busybox
              command: ["sh", "-c", "echo init && sleep 5"]
```

### Spec フィールド説明

| フィールド | 型 | 説明 |
|-----------|----|------|
| `jobTemplate` | JobTemplateSpec | 実行したい Job のテンプレート。InitJob の「望ましい init 処理」を表現する。 |

**ポイント**

- v1alpha1 では、ユーザーが制御するリトライポリシーなどは Job 側の `backoffLimit` やコンテナ内ロジックに委ねる
- 「いつ再実行するか」のポリシーは、InitJob 自体の spec ではなく **差分検知ロジック** によって決まる

---

## InitJob CR — Status

```yaml
status:
  phase: Succeeded   # Pending / Running / Succeeded / Failed
  jobName: sample-init-job-78c9c7
  lastCompletionTime: "2025-01-02T15:04:05Z"
  lastSucceeded: true
  lastAppliedJobTemplateHash: "sha256:abcd..."
  conditions:
    - type: Ready
      status: "True"
      reason: JobCompleted
      message: Job succeeded.
```

### Status フィールド説明

| フィールド | 説明 |
|-----------|------|
| `phase` | InitJob のフェーズ (`Pending` / `Running` / `Succeeded` / `Failed`) |
| `jobName` | 直近で紐づいていた Job の名前 |
| `lastCompletionTime` | 最後に Job が完了した時刻 |
| `lastSucceeded` | 最後の Job 実行が成功したかどうか |
| `lastAppliedJobTemplateHash` | 実行時に使用した `spec.jobTemplate` のハッシュ値（差分検知用） |
| `conditions` | Ready / Failed / Running などの詳細状態 |

---

## 差分検知の仕様（重要）

InitJob Operator は **「InitJob の spec が前回実行時から変わったかどうか」** を元に、Job を再作成するか判定する。

### ハッシュの扱い

- Reconcile 時に、現在の `spec.jobTemplate` から **安定したハッシュ (`sha256`)** を計算する
- その値を `status.lastAppliedJobTemplateHash` と比較する

### Job 再実行の条件

**Job を新しく作成する条件：**

1. InitJob が新規作成された（`status.lastAppliedJobTemplateHash` が空）
2. `spec.jobTemplate` のハッシュ値が `status.lastAppliedJobTemplateHash` と異なる
   → 前回実行した Job とは **内容が変わっている** とみなす

このとき、既存の Job が残っていても以下のように扱う：

- 既存 Job が `Running` / `Pending` の場合
  → v1alpha1 では「変更不可」とし、差分は status / event に記録（破壊的変更を避ける）
- 既存 Job が `Completed`(`Succeeded`/`Failed`) または存在しない場合
  → 新しい Job を作り直し、`jobName` と `lastAppliedJobTemplateHash` を更新

**Job を作らない条件：**

- `spec.jobTemplate` のハッシュ値が `status.lastAppliedJobTemplateHash` と同じ
  → 「前回と同じ init 処理」とみなし、Job が既に削除されていても **新しい Job は作らない**

これにより、

- 「マニフェストを apply し直すだけでは再実行されない」
- 「init 処理の内容（jobTemplate）が変わったときだけ再実行される」

という挙動になる。

---

## コントローラー動作仕様

### 監視対象

- `For(&InitJob{})`
- `Owns(&batchv1.Job{})`

### Reconcile フロー（ざっくり）

1. InitJob を取得
2. 現在の `spec.jobTemplate` からハッシュを計算（例: `hash := sha256(jobTemplate)`）
3. `status.lastAppliedJobTemplateHash` と比較
4. Job の存在・状態を確認
5. 以下の条件に応じて分岐：

#### 初回実行

- 条件：
  - `status.lastAppliedJobTemplateHash` が空
- 動作：
  - Job 名を決定（例: `<initjob-name>-<短いハッシュ>`）
  - Job を作成
  - `status.jobName` と `status.lastAppliedJobTemplateHash` を更新
  - `phase = Pending/Running` に遷移

#### 差分あり & Job が完了済み or 存在しない

- 条件：
  - `currentHash != status.lastAppliedJobTemplateHash`
  - かつ Job が存在しない、または `Succeeded/Failed`
- 動作：
  - 新しい Job を作成（古い Job が残っていれば削除 or 放置は実装方針次第）
  - `status.jobName` と `status.lastAppliedJobTemplateHash` を新しい値で更新
  - `phase` を `Pending/Running` に更新

#### 差分あり & Job がまだ実行中

- 条件：
  - `currentHash != status.lastAppliedJobTemplateHash`
  - かつ Job が `Active`（実行中）
- 動作：
  - v1alpha1 では **Job をいじらない**（強制再実行はしない）
  - `conditions` に「SpecChangedWhileRunning」のような Condition を積む
  - 運用者が Job / InitJob を更新することで対処

#### 差分なし（再実行なし）

- 条件：
  - `currentHash == status.lastAppliedJobTemplateHash`
- 動作：
  - Job が無くても **新しい Job は作らない**
  - `status` のみ現在の Job 状態に合わせて更新（存在しない場合は `phase` は直近結果のまま）

---

## オブザーバビリティ

### メトリクス例

- `initjob_reconcile_total`
- `initjob_reconcile_errors_total`
- `initjob_job_executions_total`（Job を新規作成した回数）
- `initjob_job_diff_reexecutions_total`（差分検知による再実行回数）

### ロギング

- `initjob`, `namespace`, `jobName`, `phase`, `hash(current)`, `hash(lastApplied)` などをログ出力
- 差分検知時には必ずログを残す

---

## 想定フェイルモードと対策

| フェイルモード | 原因 | 対策 |
|----------------|------|------|
| Job が無限に再実行される | 実際には template が毎回変わっている（timestamp 等） | jobTemplate には可変値を入れない / 変わりやすいフィールドをハッシュ対象から除外 |
| Job が一向に再実行されない | 差分が出ない形で更新している | 差分判定に使うフィールドをドキュメント化し、必要に応じて仕様を拡張 |
| 実行中に spec を変えてしまう | Running 中の差分 update | v1alpha1 では挙動をシンプルに保ち「触らない」方針とし、Condition とログで通知 |

---

## サンプル

```yaml
apiVersion: batch.init.sre.example.com/v1alpha1
kind: InitJob
metadata:
  name: migrate-db
spec:
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: migration
              image: alpine
              command: ["sh", "-c", "echo running migration && ./migrate.sh"]
```

- 最初の apply → Job 実行
- `command` を変更して再 `apply` → 差分検知 → Job 再実行
- 何も変えずに再 `apply` → Job は再作成されない（差分なし）

---

## まとめ

InitJob Operator は、

- InitJob CR が **Job の定義と実行履歴のソースオブトゥルース**
- Job は「その時点の InitJob 内容で一度だけ実行される実行体」
- **Job が消えても CR は残り、差分があるときだけ再実行される**

というポリシーで設計されている。

この挙動により、
「init のロジックを変えたときだけ再実行し、ただの `apply` では余計な Job を動かさない」
という安全な運用が可能になる。
