/**
 * Chain access for the BridgeSafe console.
 *
 * Reads go through viem against Coston2. Writes go through the browser wallet,
 * because every write here is an action only the treasury owner may take —
 * creating a treasury, opening a request, asking for a signature. The UI holds
 * no key of its own.
 */
import {
  createPublicClient,
  createWalletClient,
  custom,
  http,
  defineChain,
  type Address,
  type Hex,
} from "viem";

export const coston2 = defineChain({
  id: 114,
  name: "Flare Coston2",
  nativeCurrency: { name: "Coston2 Flare", symbol: "C2FLR", decimals: 18 },
  rpcUrls: { default: { http: ["https://coston2-api.flare.network/ext/C/rpc"] } },
  blockExplorers: {
    default: { name: "Coston2 Explorer", url: "https://coston2-explorer.flare.network" },
  },
  testnet: true,
});

export const XRPL_EXPLORER = "https://testnet.xrpl.org";
export const PAYLOAD_BUILDER = process.env.NEXT_PUBLIC_PAYLOAD_BUILDER ?? "http://127.0.0.1:8110";

export const CONTROLLER_ADDRESS = (process.env.NEXT_PUBLIC_CONTROLLER ?? "") as Address;
export const VERIFIER_ADDRESS = (process.env.NEXT_PUBLIC_FDC_VERIFIER ?? "") as Address;

export const publicClient = createPublicClient({ chain: coston2, transport: http() });

export function walletClient() {
  if (typeof window === "undefined" || !(window as any).ethereum) {
    throw new Error(
      "No browser wallet found. BridgeSafe writes are signed by the treasury owner, so a wallet on Coston2 is required."
    );
  }
  return createWalletClient({ chain: coston2, transport: custom((window as any).ethereum) });
}

/** Request lifecycle, matching BridgeSafeController.RequestState. */
export const REQUEST_STATES = [
  "None",
  "Created",
  "Authorized",
  "Signed",
  "Broadcast",
  "Settled",
  "Expired",
  "Cancelled",
  "Failed",
] as const;

export type RequestState = (typeof REQUEST_STATES)[number];

export const controllerAbi = [
  {
    type: "function",
    name: "createTreasury",
    stateMutability: "payable",
    inputs: [
      {
        name: "_policy",
        type: "tuple",
        components: [
          { name: "maxPerPaymentDrops", type: "uint256" },
          { name: "maxTotalDrops", type: "uint256" },
          { name: "requestTtlSeconds", type: "uint64" },
        ],
      },
    ],
    outputs: [{ name: "treasuryId", type: "uint256" }],
  },
  {
    type: "function",
    name: "createPaymentRequest",
    stateMutability: "payable",
    inputs: [
      { name: "_treasuryId", type: "uint256" },
      { name: "_encryptedPayload", type: "bytes" },
    ],
    outputs: [{ name: "requestId", type: "uint256" }],
  },
  {
    type: "function",
    name: "requestSignature",
    stateMutability: "payable",
    inputs: [{ name: "_requestId", type: "uint256" }],
    outputs: [],
  },
  {
    type: "function",
    name: "cancelRequest",
    stateMutability: "nonpayable",
    inputs: [{ name: "_requestId", type: "uint256" }],
    outputs: [],
  },
  {
    type: "function",
    name: "treasuryCount",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "availableDrops",
    stateMutability: "view",
    inputs: [{ name: "_treasuryId", type: "uint256" }],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "getTreasuryRequests",
    stateMutability: "view",
    inputs: [{ name: "_treasuryId", type: "uint256" }],
    outputs: [{ name: "", type: "uint256[]" }],
  },
  {
    type: "function",
    name: "getTreasury",
    stateMutability: "view",
    inputs: [{ name: "_treasuryId", type: "uint256" }],
    outputs: [
      {
        name: "",
        type: "tuple",
        components: [
          { name: "owner", type: "address" },
          { name: "xrplAddress", type: "string" },
          { name: "xrplAddressHash", type: "bytes32" },
          {
            name: "policy",
            type: "tuple",
            components: [
              { name: "maxPerPaymentDrops", type: "uint256" },
              { name: "maxTotalDrops", type: "uint256" },
              { name: "requestTtlSeconds", type: "uint64" },
            ],
          },
          { name: "policyCommitment", type: "bytes32" },
          { name: "reservedDrops", type: "uint256" },
          { name: "nextNonce", type: "uint64" },
          { name: "bound", type: "bool" },
          { name: "paused", type: "bool" },
          { name: "exists", type: "bool" },
        ],
      },
    ],
  },
  {
    type: "function",
    name: "getRequest",
    stateMutability: "view",
    inputs: [{ name: "_requestId", type: "uint256" }],
    outputs: [
      {
        name: "",
        type: "tuple",
        components: [
          { name: "treasuryId", type: "uint256" },
          { name: "requester", type: "address" },
          { name: "nonce", type: "uint64" },
          { name: "createdAt", type: "uint64" },
          { name: "expiresAt", type: "uint64" },
          { name: "payloadHash", type: "bytes32" },
          { name: "memoRef", type: "bytes32" },
          { name: "amountDrops", type: "uint256" },
          { name: "destinationHash", type: "bytes32" },
          { name: "expectedTxId", type: "bytes32" },
          { name: "signedBlobHash", type: "bytes32" },
          { name: "xrplTxId", type: "bytes32" },
          { name: "state", type: "uint8" },
        ],
      },
    ],
  },
] as const;

export type Treasury = {
  owner: Address;
  xrplAddress: string;
  xrplAddressHash: Hex;
  policy: { maxPerPaymentDrops: bigint; maxTotalDrops: bigint; requestTtlSeconds: bigint };
  policyCommitment: Hex;
  reservedDrops: bigint;
  nextNonce: bigint;
  bound: boolean;
  paused: boolean;
  exists: boolean;
};

export type PaymentRequest = {
  treasuryId: bigint;
  requester: Address;
  nonce: bigint;
  createdAt: bigint;
  expiresAt: bigint;
  payloadHash: Hex;
  memoRef: Hex;
  amountDrops: bigint;
  destinationHash: Hex;
  expectedTxId: Hex;
  signedBlobHash: Hex;
  xrplTxId: Hex;
  state: number;
};

/** XRP drops -> a readable XRP figure. 1 XRP = 1,000,000 drops. */
export function formatXrp(drops: bigint): string {
  const whole = drops / 1_000_000n;
  const frac = drops % 1_000_000n;
  if (frac === 0n) return `${whole}`;
  return `${whole}.${frac.toString().padStart(6, "0").replace(/0+$/, "")}`;
}

export function toDrops(xrp: string): bigint {
  const [whole, frac = ""] = xrp.trim().split(".");
  const padded = (frac + "000000").slice(0, 6);
  return BigInt(whole || "0") * 1_000_000n + BigInt(padded || "0");
}

export function shorten(value: string, lead = 10, tail = 8): string {
  if (!value) return "";
  if (value.length <= lead + tail + 1) return value;
  return `${value.slice(0, lead)}…${value.slice(-tail)}`;
}

const ZERO32 = "0x0000000000000000000000000000000000000000000000000000000000000000";
export const isZero32 = (v: string) => !v || v.toLowerCase() === ZERO32;

/** Seal a payment instruction to the enclave via the local payload builder. */
export async function sealPayload(input: {
  chainId: number;
  controller: Address;
  treasuryId: string;
  requestId: string;
  destination: string;
  amountDrops: string;
  reference: string;
}): Promise<{ ciphertext: Hex; payloadHash: Hex; plaintextSize: number }> {
  const res = await fetch(`${PAYLOAD_BUILDER}/seal`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      chainId: input.chainId,
      controller: input.controller,
      treasuryId: input.treasuryId,
      requestId: input.requestId,
      destination: input.destination,
      amountDrops: input.amountDrops,
      destinationTag: 0,
      hasDestinationTag: false,
      reference: input.reference,
      teePublicKey: "",
    }),
  });
  const body = await res.json();
  if (!res.ok) throw new Error(body.error ?? `payload builder returned ${res.status}`);
  return body;
}
