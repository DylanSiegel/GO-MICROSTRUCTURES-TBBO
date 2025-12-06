This is the **Final Reference Specification** for the 20-Atom Pure TBBO Microstructure set.

This list is formatted for immediate implementation by Quantitative Developers. It groups the atoms by their physical role in the market (Flow, Friction, Structure, Time) and provides the strict logical requirements for calculation.

### 1. The Raw Normalization Layer
*Before calculating atoms, map the raw TBBO record to these standardized variables.*

| Symbol | Raw Field | Transform / Unit |
|:---|:---|:---|
| $p_t$ | `price` | `x * 1e-9` (Float64) |
| $q_t$ | `size` | `uint32` (no transform) |
| $s_t$ | `side` | `B`$\to 1$, `A`$\to -1$, `N`$\to 0$ |
| $a_t, b_t$ | `ask_px_00`, `bid_px_00` | `x * 1e-9` |
| $A_t, B_t$ | `ask_sz_00`, `bid_sz_00` | `uint32` (no transform) |
| $N^A_t, N^B_t$ | `ask_ct_00`, `bid_ct_00` | `uint32` (no transform) |
| $\tau_{eng}$ | `ts_event` | `uint64` (nanoseconds) |
| $\tau_{cap}$ | `ts_recv` | `uint64` (nanoseconds) |
| $\delta_{send}$ | `ts_in_delta` | `int32` (nanoseconds) |

---

### 2. The 20 Pure Atoms (Categorized)

#### Group A: Momentum & Flow (The "Force")
*Atoms describing the energy and direction of the trade.*

| # | Atom Key | Formula | Physics Note |
|---|---|---|---|
| **1** | `signed_vol` | $q_t \cdot s_t$ | Net force exerted on the book. |
| **2** | `trade_sign` | $s_t$ | Directional vote (tick test). |
| **3** | `price_impact` | $p_t - p_{t-1}$ | **(Stateful)** Absolute price dislocation. |
| **4** | `signed_velocity` | $\frac{q_t \cdot s_t}{\max(1, \tau_{eng}(t) - \tau_{eng}(t-1))}$ | **(Stateful)** Volume throughput per nanosecond. |
| **5** | `whale_shock` | $1$ if $q_t > (A_t + B_t)$ else $0$ | Did the trade smash through all visible top liquidity? |
| **6** | `pressure_align` | $s_t \cdot \text{VolumeImbalance}_t$ | **Confluence.** Does the trade match the book skew? |

#### Group B: Friction & Liquidity (The "Surface")
*Atoms describing the resistance the market offers.*

| # | Atom Key | Formula | Physics Note |
|---|---|---|---|
| **7** | `quoted_spread` | $a_t - b_t$ | Explicit cost of trading immediately. |
| **8** | `effective_spread` | $s_t \cdot (p_t - \text{Mid}_t)$ | Realized cost (slippage) vs the midpoint. |
| **9** | `instant_amihud` | $\frac{|p_t - p_{t-1}|}{\max(1, q_t)}$ | **(Stateful)** Fragility: Price move per unit volume. |
| **10** | `vol_imbalance` | $\frac{B_t - A_t}{B_t + A_t + \epsilon}$ | Size skew. Positive = Strong Bid wall. |
| **11** | `count_imbalance` | $\frac{N^B_t - N^A_t}{N^B_t + N^A_t + \epsilon}$ | Order count skew. Positive = More buyers queuing. |

#### Group C: Valuation & Psychology (The "Signal")
*Atoms describing fair value and behavioral biases.*

| # | Atom Key | Formula | Physics Note |
|---|---|---|---|
| **12** | `mid_price` | $\frac{a_t + b_t}{2}$ | Geometric center of the spread. |
| **13** | `micro_price` | $\frac{b_t A_t + a_t B_t}{A_t + B_t + \epsilon}$ | Volume-weighted fair price (WMP). |
| **14** | `micro_dev` | $s_t \cdot (p_t - \text{MicroPrice}_t)$ | Execution quality relative to liquidity-weighted mid. |
| **15** | `cent_magnet` | $\frac{1}{1 + 100 \cdot \min(\text{frac}, 1-\text{frac})}$ | **Psychology.** Attraction to round numbers. (See code below for `frac`). |
| **16** | `avg_sz_bid` | $\frac{B_t}{\max(1, N^B_t)}$ | Iceberg/Block detector on Bid. |
| **17** | `avg_sz_ask` | $\frac{A_t}{\max(1, N^A_t)}$ | Iceberg/Block detector on Ask. |

#### Group D: Time & Latency (The "Clock")
*Atoms describing speed and system health.*

| # | Atom Key | Formula | Physics Note |
|---|---|---|---|
| **18** | `inter_trade_dur` | $\tau_{eng}(t) - \tau_{eng}(t-1)$ | **(Stateful)** Time gap. Low = High Urgency. |
| **19** | `capture_lat` | $\tau_{cap} - \tau_{eng}$ | Propagation delay (Exchange $\to$ Capture). |
| **20** | `send_delta` | $\delta_{send}$ | Internal matching engine processing lag. |

---
