package lease

import (
"context"
"errors"
"fmt"
"strconv"
"sync"
"time"

"github.com/aws/aws-sdk-go-v2/aws"
"github.com/aws/aws-sdk-go-v2/service/dynamodb"
"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
ErrLeaseHeld = errors.New("lease currently held by another active worker")
ErrFencedOut = errors.New("fenced out: lease expired or superseded by higher fencing token")
)

type Lease struct {
RunID        string `json:"run_id"`
OwnerID      string `json:"owner_id"`
FencingToken int64  `json:"fencing_token"`
ExpiresAtMs  int64  `json:"expires_at_ms"`
}

type Manager interface {
Acquire(ctx context.Context, runID, ownerID string, ttl time.Duration) (Lease, error)
VerifyAndRenew(ctx context.Context, runID, ownerID string, token int64, ttl time.Duration) (Lease, error)
Release(ctx context.Context, runID, ownerID string, token int64) error
}

// DynamoManager implements conditional-write leader leases with monotonic fencing tokens in DynamoDB.
type DynamoManager struct {
Client *dynamodb.Client
Table  string
}

func (d *DynamoManager) Acquire(ctx context.Context, runID, ownerID string, ttl time.Duration) (Lease, error) {
now := time.Now().UTC()
nowMs := now.UnixMilli()
expMs := now.Add(ttl).UnixMilli()

out, err := d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
TableName: aws.String(d.Table),
Key: map[string]types.AttributeValue{
"run_id": &types.AttributeValueMemberS{Value: runID},
},
UpdateExpression: aws.String("SET owner_id = :owner, lease_expires_at = :exp, updated_at = :now_iso ADD fencing_token :one"),
ConditionExpression: aws.String("attribute_not_exists(run_id) OR lease_expires_at < :now_ms"),
ExpressionAttributeValues: map[string]types.AttributeValue{
":owner":   &types.AttributeValueMemberS{Value: ownerID},
":exp":     &types.AttributeValueMemberN{Value: strconv.FormatInt(expMs, 10)},
":now_ms":  &types.AttributeValueMemberN{Value: strconv.FormatInt(nowMs, 10)},
":now_iso": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
":one":     &types.AttributeValueMemberN{Value: "1"},
},
ReturnValues: types.ReturnValueAllNew,
})
if err != nil {
var ccf *types.ConditionalCheckFailedException
if errors.As(err, &ccf) {
return Lease{}, ErrLeaseHeld
}
return Lease{}, fmt.Errorf("dynamodb acquire lease: %w", err)
}
return parseLease(out.Attributes)
}

func (d *DynamoManager) VerifyAndRenew(ctx context.Context, runID, ownerID string, token int64, ttl time.Duration) (Lease, error) {
now := time.Now().UTC()
nowMs := now.UnixMilli()
expMs := now.Add(ttl).UnixMilli()

out, err := d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
TableName: aws.String(d.Table),
Key: map[string]types.AttributeValue{
"run_id": &types.AttributeValueMemberS{Value: runID},
},
UpdateExpression:    aws.String("SET lease_expires_at = :exp, updated_at = :now_iso"),
ConditionExpression: aws.String("owner_id = :owner AND fencing_token = :token AND lease_expires_at >= :now_ms"),
ExpressionAttributeValues: map[string]types.AttributeValue{
":owner":   &types.AttributeValueMemberS{Value: ownerID},
":token":   &types.AttributeValueMemberN{Value: strconv.FormatInt(token, 10)},
":exp":     &types.AttributeValueMemberN{Value: strconv.FormatInt(expMs, 10)},
":now_ms":  &types.AttributeValueMemberN{Value: strconv.FormatInt(nowMs, 10)},
":now_iso": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
},
ReturnValues: types.ReturnValueAllNew,
})
if err != nil {
var ccf *types.ConditionalCheckFailedException
if errors.As(err, &ccf) {
return Lease{}, ErrFencedOut
}
return Lease{}, fmt.Errorf("dynamodb verify/renew lease: %w", err)
}
return parseLease(out.Attributes)
}

func (d *DynamoManager) Release(ctx context.Context, runID, ownerID string, token int64) error {
now := time.Now().UTC()
_, err := d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
TableName: aws.String(d.Table),
Key: map[string]types.AttributeValue{
"run_id": &types.AttributeValueMemberS{Value: runID},
},
UpdateExpression:    aws.String("SET lease_expires_at = :zero, updated_at = :now_iso"),
ConditionExpression: aws.String("owner_id = :owner AND fencing_token = :token"),
ExpressionAttributeValues: map[string]types.AttributeValue{
":owner":   &types.AttributeValueMemberS{Value: ownerID},
":token":   &types.AttributeValueMemberN{Value: strconv.FormatInt(token, 10)},
":zero":    &types.AttributeValueMemberN{Value: "0"},
":now_iso": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
},
})
if err != nil {
var ccf *types.ConditionalCheckFailedException
if errors.As(err, &ccf) {
return ErrFencedOut
}
return fmt.Errorf("dynamodb release lease: %w", err)
}
return nil
}

func parseLease(attrs map[string]types.AttributeValue) (Lease, error) {
var l Lease
if v, ok := attrs["run_id"].(*types.AttributeValueMemberS); ok {
l.RunID = v.Value
}
if v, ok := attrs["owner_id"].(*types.AttributeValueMemberS); ok {
l.OwnerID = v.Value
}
if v, ok := attrs["fencing_token"].(*types.AttributeValueMemberN); ok {
tok, err := strconv.ParseInt(v.Value, 10, 64)
if err != nil {
return Lease{}, err
}
l.FencingToken = tok
}
if v, ok := attrs["lease_expires_at"].(*types.AttributeValueMemberN); ok {
exp, err := strconv.ParseInt(v.Value, 10, 64)
if err != nil {
return Lease{}, err
}
l.ExpiresAtMs = exp
}
return l, nil
}

// MemoryManager is an in-memory conditional-write lease store for fast offline unit tests.
type MemoryManager struct {
mu     sync.Mutex
leases map[string]Lease
}

func NewMemoryManager() *MemoryManager {
return &MemoryManager{leases: make(map[string]Lease)}
}

func (m *MemoryManager) Acquire(_ context.Context, runID, ownerID string, ttl time.Duration) (Lease, error) {
m.mu.Lock()
defer m.mu.Unlock()
nowMs := time.Now().UTC().UnixMilli()
cur, exists := m.leases[runID]
if exists && cur.ExpiresAtMs >= nowMs {
return Lease{}, ErrLeaseHeld
}
next := Lease{
RunID:        runID,
OwnerID:      ownerID,
FencingToken: cur.FencingToken + 1,
ExpiresAtMs:  nowMs + ttl.Milliseconds(),
}
m.leases[runID] = next
return next, nil
}

func (m *MemoryManager) VerifyAndRenew(_ context.Context, runID, ownerID string, token int64, ttl time.Duration) (Lease, error) {
m.mu.Lock()
defer m.mu.Unlock()
nowMs := time.Now().UTC().UnixMilli()
cur, exists := m.leases[runID]
if !exists || cur.OwnerID != ownerID || cur.FencingToken != token || cur.ExpiresAtMs < nowMs {
return Lease{}, ErrFencedOut
}
cur.ExpiresAtMs = nowMs + ttl.Milliseconds()
m.leases[runID] = cur
return cur, nil
}

func (m *MemoryManager) Release(_ context.Context, runID, ownerID string, token int64) error {
m.mu.Lock()
defer m.mu.Unlock()
cur, exists := m.leases[runID]
if !exists || cur.OwnerID != ownerID || cur.FencingToken != token {
return ErrFencedOut
}
cur.ExpiresAtMs = 0
m.leases[runID] = cur
return nil
}
