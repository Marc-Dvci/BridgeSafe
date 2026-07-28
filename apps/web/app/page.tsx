"use client";

import { useCallback, useEffect, useState } from "react";
import type { Address } from "viem";

import Trace from "@/components/Trace";
import {
  CONTROLLER_ADDRESS,
  VERIFIER_ADDRESS,
  REQUEST_STATES,
  XRPL_EXPLORER,
  controllerAbi,
  coston2,
  formatXrp,
  isZero32,
  publicClient,
  sealPayload,
  shorten,
  toDrops,
  walletClient,
  type PaymentRequest,
  type Treasury,
} from "@/lib/bridgesafe";

const INSTRUCTION_FEE = 10_000_000_000_000_000n; // 0.01 C2FLR, forwarded to the TEE registry

export default function Console() {
  const [account, setAccount] = useState<Address | null>(null);
  const [treasuryId, setTreasuryId] = useState<string>("1");
  const [treasury, setTreasury] = useState<Treasury | null>(null);
  const [requestIds, setRequestIds] = useState<bigint[]>([]);
  const [selected, setSelected] = useState<bigint | null>(null);
  const [request, setRequest] = useState<PaymentRequest | null>(null);

  const [destination, setDestination] = useState("");
  const [amount, setAmount] = useState("25");
  const [reference, setReference] = useState("contractor invoice");

  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);

  const configured = Boolean(CONTROLLER_ADDRESS);

  /* ---------------------------------------------------------------- reads */

  const loadTreasury = useCallback(async () => {
    if (!configured) return;
    try {
      const id = BigInt(treasuryId || "0");
      if (id === 0n) return;
      const t = (await publicClient.readContract({
        address: CONTROLLER_ADDRESS,
        abi: controllerAbi,
        functionName: "getTreasury",
        args: [id],
      })) as unknown as Treasury;
      setTreasury(t);

      const ids = (await publicClient.readContract({
        address: CONTROLLER_ADDRESS,
        abi: controllerAbi,
        functionName: "getTreasuryRequests",
        args: [id],
      })) as unknown as bigint[];
      setRequestIds([...ids].reverse());
      setError(null);
    } catch (e: any) {
      setTreasury(null);
      setRequestIds([]);
      setError(
        `Could not read treasury ${treasuryId}. ${
          e?.shortMessage ?? e?.message ?? ""
        }`
      );
    }
  }, [treasuryId, configured]);

  const loadRequest = useCallback(async (id: bigint | null) => {
    if (!configured || id === null) return setRequest(null);
    try {
      const r = (await publicClient.readContract({
        address: CONTROLLER_ADDRESS,
        abi: controllerAbi,
        functionName: "getRequest",
        args: [id],
      })) as unknown as PaymentRequest;
      setRequest(r);
    } catch {
      setRequest(null);
    }
  }, [configured]);

  useEffect(() => { void loadTreasury(); }, [loadTreasury]);

  // Poll while a request is mid-flight. Settlement lands asynchronously, when the
  // FDC round finalises, so the UI has to keep watching rather than assume.
  useEffect(() => {
    void loadRequest(selected);
    if (selected === null) return;
    const t = setInterval(() => {
      void loadRequest(selected);
      void loadTreasury();
    }, 6000);
    return () => clearInterval(t);
  }, [selected, loadRequest, loadTreasury]);

  /* --------------------------------------------------------------- writes */

  async function connect() {
    try {
      const wc = walletClient();
      const [a] = await wc.requestAddresses();
      await wc.switchChain({ id: coston2.id }).catch(async () => {
        await (window as any).ethereum.request({
          method: "wallet_addEthereumChain",
          params: [{
            chainId: "0x72",
            chainName: "Flare Coston2",
            nativeCurrency: { name: "Coston2 Flare", symbol: "C2FLR", decimals: 18 },
            rpcUrls: coston2.rpcUrls.default.http,
            blockExplorerUrls: [coston2.blockExplorers.default.url],
          }],
        });
      });
      setAccount(a);
      setError(null);
    } catch (e: any) {
      setError(e?.message ?? "Could not connect a wallet.");
    }
  }

  async function submitPayment() {
    setError(null);
    setInfo(null);
    if (!account) return setError("Connect the treasury owner's wallet first.");
    if (!treasury?.bound) return setError("This treasury has no enclave-generated XRPL address yet.");

    setBusy("Opening the request on Flare…");
    try {
      const wc = walletClient();
      const id = BigInt(treasuryId);
      const drops = toDrops(amount);

      // The request id must be known before sealing, because the enclave refuses
      // a payload that names a different request. Simulating the call gives us
      // the id the transaction will actually assign.
      const { result: nextRequestId } = await publicClient.simulateContract({
        address: CONTROLLER_ADDRESS,
        abi: controllerAbi,
        functionName: "createPaymentRequest",
        args: [id, "0x00"],
        account,
        value: INSTRUCTION_FEE,
      });

      setBusy("Sealing the instruction to the enclave…");
      const sealed = await sealPayload({
        chainId: coston2.id,
        controller: CONTROLLER_ADDRESS,
        treasuryId: id.toString(),
        requestId: (nextRequestId as bigint).toString(),
        destination,
        amountDrops: drops.toString(),
        reference,
      });

      setBusy("Confirm in your wallet…");
      const hash = await wc.writeContract({
        address: CONTROLLER_ADDRESS,
        abi: controllerAbi,
        functionName: "createPaymentRequest",
        args: [id, sealed.ciphertext],
        account,
        chain: coston2,
        value: INSTRUCTION_FEE,
      });

      setBusy("Waiting for Flare…");
      await publicClient.waitForTransactionReceipt({ hash });

      setSelected(nextRequestId as bigint);
      setInfo(
        `Request ${nextRequestId} opened. The enclave now decrypts the instruction and checks it against the policy.`
      );
      await loadTreasury();
    } catch (e: any) {
      setError(e?.shortMessage ?? e?.message ?? "Transaction failed.");
    } finally {
      setBusy(null);
    }
  }

  async function askForSignature() {
    if (!account || selected === null) return;
    setError(null);
    setBusy("Requesting the enclave signature…");
    try {
      const wc = walletClient();
      const hash = await wc.writeContract({
        address: CONTROLLER_ADDRESS,
        abi: controllerAbi,
        functionName: "requestSignature",
        args: [selected],
        account,
        chain: coston2,
        value: INSTRUCTION_FEE,
      });
      await publicClient.waitForTransactionReceipt({ hash });
      setInfo("Signature requested. The enclave will build and sign the XRPL payment.");
      await loadRequest(selected);
    } catch (e: any) {
      setError(e?.shortMessage ?? e?.message ?? "Transaction failed.");
    } finally {
      setBusy(null);
    }
  }

  /* ----------------------------------------------------------------- view */

  const stateName = request ? REQUEST_STATES[request.state] ?? "Unknown" : "";
  const pillClass =
    request?.state === 5 ? "settled" : request && request.state >= 6 ? "dead" : "progress";

  return (
    <main className="shell">
      <header className="top">
        <div className="brand">
          <h1>BridgeSafe<span className="dot">.</span></h1>
          <span className="badge">Coston2 · XRPL Testnet</span>
        </div>
        <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
          {account ? (
            <span className="badge">{shorten(account, 6, 4)}</span>
          ) : (
            <button onClick={connect} style={{ marginTop: 0 }}>Connect wallet</button>
          )}
        </div>
      </header>

      <p className="tagline">
        An XRPL treasury controlled from Flare. Payment instructions stay encrypted until a confidential
        enclave checks them against a published spending policy and signs; settlement is not believed until
        the Flare Data Connector proves the payment actually happened.
      </p>

      {!configured && (
        <div className="notice error">
          <strong>Not configured.</strong> Set <code>NEXT_PUBLIC_CONTROLLER</code> and{" "}
          <code>NEXT_PUBLIC_FDC_VERIFIER</code> in <code>apps/web/.env.local</code> — see{" "}
          <code>docs/demo-script.md</code>.
        </div>
      )}

      <div className="grid">
        {/* ------------------------------------------------ treasury */}
        <section className="card">
          <h2>Treasury</h2>

          <label htmlFor="tid">Treasury id</label>
          <input id="tid" value={treasuryId} onChange={(e) => setTreasuryId(e.target.value)} />

          {treasury && (
            <div style={{ marginTop: 18 }}>
              <div className="row">
                <span className="k">Owner</span>
                <span className="v">{shorten(treasury.owner, 8, 6)}</span>
              </div>
              <div className="row">
                <span className="k">XRPL account</span>
                <span className="v">
                  {treasury.bound ? (
                    <a href={`${XRPL_EXPLORER}/accounts/${treasury.xrplAddress}`} target="_blank" rel="noreferrer">
                      {treasury.xrplAddress}
                    </a>
                  ) : (
                    <span style={{ color: "var(--pending)" }}>awaiting enclave key</span>
                  )}
                </span>
              </div>
              <div className="row">
                <span className="k">Per-payment cap</span>
                <span className="v">{formatXrp(treasury.policy.maxPerPaymentDrops)} XRP</span>
              </div>
              <div className="row">
                <span className="k">Lifetime cap</span>
                <span className="v">{formatXrp(treasury.policy.maxTotalDrops)} XRP</span>
              </div>
              <div className="row">
                <span className="k">Spent / reserved</span>
                <span className="v">{formatXrp(treasury.reservedDrops)} XRP</span>
              </div>
              <div className="row">
                <span className="k">Request lifetime</span>
                <span className="v">{Number(treasury.policy.requestTtlSeconds) / 60} min</span>
              </div>

              {treasury.bound && (
                <div className="notice">
                  Fund this account with test XRP before requesting a payment —{" "}
                  <a href="https://faucet.altnet.rippletest.net/accounts" target="_blank" rel="noreferrer">
                    XRPL testnet faucet
                  </a>
                  . The private key for it exists only inside the enclave.
                </div>
              )}
            </div>
          )}
        </section>

        {/* ------------------------------------------ create payment */}
        <section className="card">
          <h2>New confidential payment</h2>

          <label htmlFor="dest">Destination (XRPL r-address)</label>
          <input
            id="dest"
            placeholder="rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe"
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
          />

          <label htmlFor="amt">Amount (XRP)</label>
          <input id="amt" value={amount} onChange={(e) => setAmount(e.target.value)} />

          <label htmlFor="ref">Reference (stays inside the enclave)</label>
          <input id="ref" value={reference} onChange={(e) => setReference(e.target.value)} />

          <button onClick={submitPayment} disabled={Boolean(busy) || !configured || !destination}>
            {busy ?? "Seal and submit"}
          </button>

          <div className="notice">
            The destination and amount are encrypted to the enclave&apos;s public key. Flare stores only the
            hash of that ciphertext, so nobody watching the chain learns who is being paid until the payment
            settles on XRPL.
          </div>

          {error && <div className="notice error">{error}</div>}
          {info && <div className="notice ok">{info}</div>}
        </section>

        {/* ------------------------------------------------ requests */}
        <section className="card">
          <h2>Requests</h2>
          {requestIds.length === 0 ? (
            <p style={{ color: "var(--muted)", fontSize: 13, margin: 0 }}>No requests yet.</p>
          ) : (
            <ul className="reqlist">
              {requestIds.map((id) => (
                <li
                  key={id.toString()}
                  className={selected === id ? "sel" : ""}
                  onClick={() => setSelected(id)}
                >
                  <span className="id">#{id.toString()}</span>
                  {selected === id && request && (
                    <span className={`pill ${pillClass}`}>{stateName}</span>
                  )}
                </li>
              ))}
            </ul>
          )}

          {request && (
            <div style={{ marginTop: 16 }}>
              <div className="row">
                <span className="k">State</span>
                <span className="v">
                  <span className={`pill ${pillClass}`}>{stateName}</span>
                </span>
              </div>
              <div className="row">
                <span className="k">Amount</span>
                <span className="v">
                  {request.amountDrops > 0n ? `${formatXrp(request.amountDrops)} XRP` : "sealed"}
                </span>
              </div>
              <div className="row">
                <span className="k">Nonce</span>
                <span className="v">{request.nonce.toString()}</span>
              </div>
              <div className="row">
                <span className="k">Expires</span>
                <span className="v">
                  {new Date(Number(request.expiresAt) * 1000).toLocaleTimeString()}
                </span>
              </div>
              {!isZero32(request.xrplTxId) && (
                <div className="row">
                  <span className="k">XRPL tx</span>
                  <span className="v">
                    <a
                      href={`${XRPL_EXPLORER}/transactions/${request.xrplTxId.slice(2).toUpperCase()}`}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {shorten(request.xrplTxId.slice(2).toUpperCase(), 10, 8)}
                    </a>
                  </span>
                </div>
              )}

              {request.state === 2 && (
                <button onClick={askForSignature} disabled={Boolean(busy)}>
                  Request enclave signature
                </button>
              )}
            </div>
          )}
        </section>

        {/* --------------------------------------------------- trace */}
        <Trace request={request} treasuryXrpl={treasury?.xrplAddress ?? ""} />
      </div>

      <footer>
        Coston2 · XRPL Testnet.{" "}
        {CONTROLLER_ADDRESS && (
          <>
            Controller{" "}
            <a
              href={`${coston2.blockExplorers.default.url}/address/${CONTROLLER_ADDRESS}`}
              target="_blank"
              rel="noreferrer"
            >
              {shorten(CONTROLLER_ADDRESS, 8, 6)}
            </a>
            {VERIFIER_ADDRESS && (
              <>
                {" · "}Verifier{" "}
                <a
                  href={`${coston2.blockExplorers.default.url}/address/${VERIFIER_ADDRESS}`}
                  target="_blank"
                  rel="noreferrer"
                >
                  {shorten(VERIFIER_ADDRESS, 8, 6)}
                </a>
              </>
            )}
          </>
        )}
      </footer>
    </main>
  );
}
