/** Composition constants and the real on-chain facts the video shows. */

export const FPS = 30;
export const WIDTH = 1920;
export const HEIGHT = 1080;

/** Must match COMPOSITION_SECONDS in scripts/generate-narration.py. */
export const DURATION_SECONDS = 190;
export const DURATION_IN_FRAMES = DURATION_SECONDS * FPS;

export const sec = (seconds: number) => Math.round(seconds * FPS);

/**
 * Everything below is real and checkable.
 *
 * The contracts are deployed and source-verified on Coston2. The XRPL payment is
 * a validated transaction produced by `TestLive_PaymentIsAcceptedByTheLedger`,
 * which generates a key the way the enclave does, signs with the hand-written
 * codec, and submits to the live testnet ledger. Nothing here is a mock-up, so
 * anyone watching can open an explorer and confirm it.
 */
export const facts = {
  controller: "0x32176FCA80690938194F30844501ea24Cf48b752",
  verifier: "0x0B1B437183571ba99a5A27E1Ac980CA2ffd5b1D8",
  fdcVerification: "0x906507E0B64bcD494Db73bd0459d1C667e14B933",

  xrplTxId: "37CED2C9C68354324616D4C8F7C93B10DC829C6E521DDD295C392DC6CC7B9891",
  treasuryXrpl: "rnCyFuEqg5xcNpagebcmyMwgKGMNNAQHDQ",
  payeeXrpl: "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe",

  coston2Explorer: "coston2-explorer.flare.network",
  xrplExplorer: "testnet.xrpl.org",

  tests: {contracts: 64, extension: 34, services: 14, total: 112},
} as const;

/** The demo payment, matching the policy in docs/demo-script.md. */
export const demo = {
  amountXrp: 25,
  perPaymentCapXrp: 100,
  lifetimeCapXrp: 500,
  ttlMinutes: 30,
  payloadHash: "0x7f5b4967a9fbe9b4…6470bfd74d34",
  memoRef: "0xe86b465a29d804df…c4c455decbd6",
  destinationHash: "0x3434678fdd3c8f74…5660ae660f7e",
  memoBytes: "42534631 e86b465a29d804df…",
} as const;
