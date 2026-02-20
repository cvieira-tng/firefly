# BSV Testnet Integration Test Report

**Date:** 2026-01-21
**Environment:** BSV Testnet
**bsvconnect URL:** http://localhost:5102
**FireFly URL:** http://localhost:5000

---

## Configuration

- **BSV Address:** `muw6hBSGsMXkyDjYdMc7z5vAjx4BLdyeXb`
- **Network:** test
- **Initial Balance:** 99,285 satoshis

### bsvconnect config.yaml
```yaml
woc:
  url: "https://api.whatsonchain.com"
  network: "test"
  rateLimit: 3
  timeout: "30s"

keys:
  signingKey: "cSF746QmLmyM83XBSJqNRVDnYqQn5PUv5bPAHgKMY9eQxsd3f5Ct"

transaction:
  feeRate: 1
  minConfirmations: 0
  dustLimit: 546

server:
  address: ":5102"
```

### firefly-bsv.yaml
```yaml
namespaces:
  predefined:
    - name: default
      defaultKey: muw6hBSGsMXkyDjYdMc7z5vAjx4BLdyeXb
      plugins:
        - database0
        - blockchain0

plugins:
  blockchain:
    - name: blockchain0
      type: bsv
      bsv:
        bsvconnect:
          url: http://localhost:5102
          topic: firefly
```

---

## Production Architecture

In production, **applications always call FireFly Core (port 5000)**. The bsvconnect service (port 5102) is an internal component.

```
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│  Your App       │ ───► │  FireFly Core   │ ───► │  bsvconnect     │ ───► BSV
│                 │      │  (port 5000)    │      │  (port 5102)    │
└─────────────────┘      └─────────────────┘      └─────────────────┘
                                │
                                ▼
                         Operation tracking
                         Multiparty messaging
                         Batching
                         Event aggregation
```

### Why Not Call bsvconnect Directly?

You **could** call bsvconnect directly, but you'd lose:
- Operation tracking (no status updates)
- Integration with FireFly's data layer
- Multiparty messaging features
- Batch pinning orchestration
- Audit trail and transaction history

### When Would You Call bsvconnect Directly?

Only for:
- Debugging/testing the connector in isolation
- Edge cases where you need raw blockchain access without FireFly overhead

In this test report, bsvconnect was called directly to verify the connector works before testing the full stack.

---

## Test Results Summary

| Phase | Test | Status |
|-------|------|--------|
| Event Stream Management | Create/List Streams | **PASS** |
| Event Stream Management | Create/List Listeners | **PASS** |
| Direct API (bsvconnect) | OP_RETURN Pin | **PASS** |
| FireFly Core Integration | Contract Invoke | **PASS** |
| Event/Receipt System | WebSocket Receipt Delivery | **PASS** |
| Event/Receipt System | Operation Status Update | **PASS** |

---

## Transaction Routing

### Direct to bsvconnect (Port 5102)

| TxID | Endpoint | Data | Fee | Confirmations |
|------|----------|------|-----|---------------|
| `acb6a6ad28742ee334624213acd64dc2f04f2e2d91a9c7b154426f5c01c4e386` | `POST /api/v1/invoke` | "Hello World" | 226 sats | 5+ |

**Request:**
```bash
curl -X POST http://localhost:5102/api/v1/invoke \
  -d '{"id":"test-op-001","method":"pin","params":{"data":"48656c6c6f20576f726c64"}}'
```

**Response:**
```json
{"id":"test-op-001","txId":"acb6a6ad28742ee334624213acd64dc2f04f2e2d91a9c7b154426f5c01c4e386"}
```

---

### Through FireFly Core (Port 5000)

| TxID | Operation ID | Data | Fee | Status |
|------|--------------|------|-----|--------|
| `513b70c768086c01253ff74a1384a3e41965e597bdbb4a1dbe9d409cfc510d68` | `6c299cea-b9d8-4960-b2dc-aef819f7db60` | "FireFily BSV Test" | 226 sats | Pending* |
| `5190f7819f083cb577003cb96a39d31521eeda4959f914c371bc682b7efd4cdf` | `b3c42d2f-0bb7-4a1a-aec1-27e923bd5cfe` | "Fixed Bug" | 226 sats | Pending* |
| `7b83af310aae04cdffb0764701f59d82212455f6022c2b17f40762d430ac5cd2` | `66e29aaf-112d-43f1-b795-dde83352167d` | "ReceiptTest" | 226 sats | Pending* |
| `07f49cc545557f8dcb81ac783424009f4e6e0f2392b6e905e9c87d718253919e` | `47901204-8229-4548-89aa-62bbcb1bebf2` | "EventTest" | 226 sats | **Succeeded** |

*Pending = Before bug fixes were applied

**Request:**
```bash
curl -X POST http://localhost:5000/api/v1/namespaces/default/contracts/invoke \
  -H 'Content-Type: application/json' \
  -d '{"method":{"name":"pin","params":[{"name":"data","schema":{"type":"string"}}]},"input":{"data":"4576656e7454657374"}}'
```

**Response (Initial):**
```json
{"id":"47901204-8229-4548-89aa-62bbcb1bebf2","status":"Pending"}
```

**Response (After Confirmation):**
```json
{
  "id": "47901204-8229-4548-89aa-62bbcb1bebf2",
  "status": "Succeeded",
  "output": {
    "transactionHash": "07f49cc545557f8dcb81ac783424009f4e6e0f2392b6e905e9c87d718253919e"
  }
}
```

---

## Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        DIRECT TO BSVCONNECT                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  curl POST :5102/api/v1/invoke ──► bsvconnect ──► WhatsOnChain ──► BSV     │
│                                        │                                    │
│                                   Returns txId                              │
│                                   immediately                               │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│                        THROUGH FIREFLY CORE                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  curl POST :5000/contracts/invoke                                           │
│         │                                                                   │
│         ▼                                                                   │
│  ┌─────────────┐    POST /invoke    ┌─────────────┐                        │
│  │ FireFly     │ ─────────────────► │ bsvconnect  │                        │
│  │ Core        │                    │             │                        │
│  │             │ ◄───── txId ────── │  Build TX   │                        │
│  │ Creates     │                    │  Sign TX    │                        │
│  │ Operation   │                    │  Broadcast  │──► WhatsOnChain ──► BSV│
│  │ (Pending)   │                    │             │                        │
│  └─────────────┘                    └─────────────┘                        │
│         │                                  │                                │
│         │                                  │ (polls for confirmation)       │
│         │                                  ▼                                │
│         │                           ┌─────────────┐                        │
│         │                           │ TX Confirmed│                        │
│         │                           └─────────────┘                        │
│         │                                  │                                │
│         │    WebSocket Receipt             │                                │
│         │    {"type":"Receipt",...}        │                                │
│         ◄──────────────────────────────────┘                                │
│         │                                                                   │
│         ▼                                                                   │
│  ┌─────────────┐                                                           │
│  │ Operation   │                                                           │
│  │ Updated to  │                                                           │
│  │ "Succeeded" │                                                           │
│  └─────────────┘                                                           │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Endpoint Verification

| Endpoint | Method | Port | Status | Notes |
|----------|--------|------|--------|-------|
| `/api/v1/eventstreams` | POST | 5102 | **200** | Returns stream ID |
| `/api/v1/eventstreams` | GET | 5102 | **200** | Returns array |
| `/api/v1/eventstreams/{id}/listeners` | POST | 5102 | **201** | Returns listener ID |
| `/api/v1/eventstreams/{id}/listeners` | GET | 5102 | **200** | Returns array |
| `/api/v1/invoke` | POST | 5102 | **200** | Returns txId |
| `/health` | GET | 5102 | **200** | `{"status":"ok"}` |
| `/api/v1/namespaces/default/contracts/invoke` | POST | 5000 | **202** | Returns operation ID |
| `/api/v1/namespaces/default/operations/{id}` | GET | 5000 | **200** | Returns operation status |

---

## Endpoint Explanations

### bsvconnect Endpoints (Port 5102)

#### 1. `POST /api/v1/eventstreams` - Create Event Stream
Creates a named stream for receiving blockchain events/receipts.
- **Purpose:** Set up a WebSocket delivery channel for events
- **Input:** Stream name, batch size, timeout
- **Output:** Stream ID (UUID)
- **Use case:** FireFly needs a stream to receive transaction receipts and confirmations

#### 2. `GET /api/v1/eventstreams` - List Event Streams
Lists all active event streams.
- **Purpose:** See what streams exist and their configuration
- **Input:** None
- **Output:** Array of stream objects
- **Use case:** Debugging, monitoring

#### 3. `POST /api/v1/eventstreams/{id}/listeners` - Create Listener
Creates a listener on a specific stream to filter events.
- **Purpose:** Subscribe to specific event types on a stream
- **Input:** Listener name, filters (e.g., only "BatchPin" events)
- **Output:** Listener ID (UUID)
- **Use case:** FireFly subscribes to batch pin events so it knows when transactions are confirmed

#### 4. `GET /api/v1/eventstreams/{id}/listeners` - List Listeners
Lists all listeners on a stream.
- **Purpose:** See what's listening to each stream
- **Input:** Stream ID
- **Output:** Array of listener objects
- **Use case:** Debugging event subscriptions

#### 5. `POST /api/v1/invoke` - Execute Smart Contract Method
Executes a blockchain operation (currently only "pin" for OP_RETURN).
- **Purpose:** Create and broadcast a blockchain transaction
- **Input:** Method name ("pin"), parameters (hex data to embed)
- **Output:** Transaction ID
- **Use case:** Store data on-chain via OP_RETURN output
- **Example:** `{"id":"test-001","method":"pin","params":{"data":"48656c6c6f"}}`

#### 6. `GET /health` - Health Check
Checks if bsvconnect is running and responsive.
- **Purpose:** Liveness/readiness probe
- **Input:** None
- **Output:** `{"status":"ok"}`
- **Use case:** Docker health checks, load balancer probes

### FireFly Core Endpoints (Port 5000)

#### 7. `POST /api/v1/namespaces/default/contracts/invoke` - Invoke Contract via FireFly
High-level endpoint that creates an operation and routes to bsvconnect.
- **Purpose:** Execute blockchain operation with operation tracking
- **Input:** Method name, parameters, namespace
- **Output:** Operation ID + status
- **Use case:** Enterprise applications need to track operation state
- **Difference from direct invoke:** Async, returns immediately with operation ID (status updates via WebSocket)

#### 8. `GET /api/v1/namespaces/default/operations/{id}` - Get Operation Status
Polls the status of a pending blockchain operation.
- **Purpose:** Check if transaction was confirmed
- **Input:** Operation ID
- **Output:** Operation status (Pending → Succeeded/Failed), transaction hash
- **Use case:** Applications wait for this to change from "Pending" to "Succeeded"

### The Two Calling Patterns

| Route | Port | Caller | Response | Best For |
|-------|------|--------|----------|----------|
| `/api/v1/invoke` | 5102 | Direct code | Immediate txId | Low-level access, testing |
| `/contracts/invoke` | 5000 | Applications | Async operation | Enterprise, auditing, tracking |

**Direct path (5102):**
```
You → bsvconnect → BSV → return txId immediately
```

**Through FireFly (5000):**
```
You → FireFly → bsvconnect → BSV → WebSocket receipt → operation updated
```

---

## Explorer Links

| Description | Link |
|-------------|------|
| Wallet | https://test.whatsonchain.com/address/muw6hBSGsMXkyDjYdMc7z5vAjx4BLdyeXb |
| TX 1 (Direct) | https://test.whatsonchain.com/tx/acb6a6ad28742ee334624213acd64dc2f04f2e2d91a9c7b154426f5c01c4e386 |
| TX 2 (Core) | https://test.whatsonchain.com/tx/513b70c768086c01253ff74a1384a3e41965e597bdbb4a1dbe9d409cfc510d68 |
| TX 3 (Core) | https://test.whatsonchain.com/tx/5190f7819f083cb577003cb96a39d31521eeda4959f914c371bc682b7efd4cdf |
| TX 4 (Core) | https://test.whatsonchain.com/tx/7b83af310aae04cdffb0764701f59d82212455f6022c2b17f40762d430ac5cd2 |
| TX 5 (Core+Events) | https://test.whatsonchain.com/tx/07f49cc545557f8dcb81ac783424009f4e6e0f2392b6e905e9c87d718253919e |

---

## Summary

| Metric | Value |
|--------|-------|
| Total Transactions | 5 |
| Direct to bsvconnect | 1 |
| Through FireFly Core | 4 |
| Event System Working | **YES** |
| Total Fees Paid | ~1,130 satoshis |

---

## Next Steps

1. Test batch pin functionality (`/api/v1/submit_batch_pin`)
2. Test multiparty messaging workflow
3. Add transaction retry logic for network failures
4. Add comprehensive error handling tests
