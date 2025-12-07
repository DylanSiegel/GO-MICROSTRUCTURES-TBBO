## 1. Out-of-sample IC (Information Coefficient)

**What:** Correlation between signal (S_t) and future mid-return (R_{t,H}), computed **out-of-sample** and in **rolling windows**.

* Metrics:

  * Pearson IC: `corr(S_t, R_{t,H})`
  * Rank IC (Spearman): `corr(rank(S_t), rank(R_{t,H}))`
* Requirements:

  * Positive, statistically significant (t-stat > ~3 over long history).
  * Stable across time (no huge regime flips).
* Why critical: This is the primary measure of predictive power. If IC is weak or unstable, everything else is cosmetic.

---

## 2. Hit rate of direction vs 50% baseline

**What:** Fraction of times the signal gets the **sign** of the future move right.

* Metric:

  * `HitRate = P(sign(S_t) = sign(R_{t,H}))`
* Requirements:

  * Out-of-sample hit rate meaningfully > 50%.
  * Confidence interval does not overlap 50% by chance (binomial test).
* Why critical: Converts correlation into an intuitive “how often am I right?” measure; useful for sanity vs implementation frictions.

---

## 3. Monotone conditional return curve (deciles)

**What:** Average future return by **signal bucket**.

* Procedure:

  * Bin (S_t) into K quantiles (e.g., 10 deciles).
  * For each decile k, compute:

    * ( \mathbb{E}[R_{t,H} \mid S_t \in Q_k] )
* Requirements:

  * Clear, roughly monotone increasing curve from low to high deciles.
  * Extremes (top/bottom deciles) show strongest positive/negative average returns.
* Why critical: Ensures the mapping “larger signal ⇒ larger expected move” is structurally sound, not just noise from linear correlation.

---

## 4. Mutual Information (MI) / Normalized MI between signal and returns

**What:** Pure information-theoretic value of the signal.

* Setup:

  * Discretize future return (R_{t,H}) into classes (e.g., Down / Flat / Up or quantiles).
  * Discretize signal (S_t) into quantile bins.
* Metrics:

  * Mutual Information:
    ( I(X;Y) = H(Y) - H(Y|X) )
  * Normalized MI:
    ( \text{NMI} = I(X;Y) / H(Y) )
* Requirements:

  * MI > 0 and stable over time.
  * NMI positive and persistent across days / regimes (even small but robust is valuable).
* Why critical: Answers “how many bits of uncertainty about future returns does this signal actually remove?” — the pure information view.

---

## 5. Δ Log-loss (Cross-entropy improvement vs baseline)

**What:** How much better your **probability forecasts** are compared with no signal.

* Baseline model:

  * Predict class probabilities of (Y) (future return class) from unconditional frequencies.
* Signal model:

  * Predict (p_\theta(Y_t | S_t)) (e.g., logistic or softmax model).
* Metric:

  * Average log-loss / cross-entropy for baseline vs signal model.
  * ΔLogLoss = baseline_loss – signal_loss.
* Requirements:

  * ΔLogLoss positive, statistically significant, and stable over time.
* Why critical: Equivalent to an information gain measure under a model; directly connected to mutual information and usable for model comparison.

---

## 6. Out-of-sample Sharpe after realistic microstructure costs

**What:** Economic value of the signal when traded in a simple way.

* Setup:

  * Simple strategy: trade in direction of signal (e.g., sign(S_t)), hold for horizon H, incorporate:

    * Spread cost
    * Estimated impact / slippage
    * Fees/rebates
* Metrics:

  * Net Sharpe ratio (annualized or per sqrt(hour/day)).
  * Hit rate of trades (P&L > 0).
* Requirements:

  * Positive Sharpe after costs, robust across time.
  * P&L not dominated by a few outlier periods.
* Why critical: Confirms that the information you measure is not fully eaten by trading frictions.

---

## 7. Risk profile: drawdown and tail behavior

**What:** “Can I survive trading this signal?”

* Metrics:

  * Max drawdown of strategy P&L.
  * Skewness, tail percentiles of trade returns.
  * Ratio of average win to average loss.
* Requirements:

  * Drawdowns acceptable for your leverage/risk.
  * Loss tail behavior understood and not catastrophic for realistic sizing.
* Why critical: You need confidence not just in edge but in survivability.

---

## 8. Stability across time & regimes (IC / MI / Sharpe by slice)

**What:** Does the signal die in certain environments?

* Slices:

  * By day / week / month.
  * By volatility regime (low/med/high).
  * By spread regime (1-tick vs wider) and time of day.
* Metrics:

  * Distribution of IC, MI, and Sharpe across slices.
* Requirements:

  * Majority of slices show positive IC / MI / Sharpe.
  * Weakness in some regimes is okay, complete collapse is not.
* Why critical: Live trading is about **stability**; if performance is concentrated in a tiny subset of history, confidence should be low.

---

## 9. Horizon decay / alpha half-life

**What:** Does the information behave as a short-horizon microstructure edge?

* Metrics:

  * IC, hit rate, MI, Sharpe for 5s, 10s, 20s horizons (and maybe 40s).
* Requirements:

  * Clear, interpretable decay as horizon increases.
  * Shape of decay consistent with microstructure intuition (strong at very short horizon, weaker as noise accumulates).
* Why critical: Confirms you are truly extracting high-frequency structure, not a fragile artifact.

---

### How to use this list

If you can honestly say, **out-of-sample**:

1. IC & hit rate are positive, statistically solid, and stable.
2. Decile conditional returns are monotone with strong extremes.
3. MI / NMI and Δ log-loss vs baseline are persistently > 0.
4. Net Sharpe after realistic costs is positive and not concentrated in a tiny window.
5. Risk (drawdown/tails) is acceptable for your sizing.
6. All of the above roughly hold across different days/regimes and show a coherent horizon decay.

…then you have a **very strong case** that your TBBO/OFI-style math is genuinely informative and has a high probability of working live.

