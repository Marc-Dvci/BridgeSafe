// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {ITeeExtensionRegistry} from "./interfaces/ITeeExtensionRegistry.sol";
import {ITeeMachineRegistry} from "./interfaces/ITeeMachineRegistry.sol";
import {TeeResult} from "./lib/TeeResult.sol";

/// @title BridgeSafeController
/// @notice Flare-side control and audit layer for an XRPL treasury whose signing key
///         lives inside a Flare Confidential Compute enclave.
///
/// @dev The contract is three things at once:
///
///      1. **Treasury registry.** Spending policies live here, so limits are public and
///         auditable even though individual payment instructions are not.
///      2. **FCC InstructionSender.** It is the only address the TeeExtensionRegistry
///         will accept instructions from for the BridgeSafe extension, which makes every
///         field it puts into an instruction authenticated-by-construction: the enclave
///         can trust `treasuryId`, `memoRef` and `expiresAt` precisely because no other
///         caller could have produced them.
///      3. **Request state machine.** Payments walk
///         `CREATED → AUTHORIZED → SIGNED → BROADCAST → SETTLED`, and `SETTLED` is
///         reachable only through `BridgeSafeFdcVerifier`, which requires a Flare Data
///         Connector proof of the actual XRPL payment.
///
///      Trust boundaries worth stating plainly: the enclave decides whether a payment
///      satisfies the policy, but it cannot mark a payment settled — only an FDC proof
///      does that. The relayer can broadcast but holds no key and cannot create or
///      authorize a payment. See SECURITY.md and docs/threat-model.md.
///
///      Testnet software. Coston2 + XRPL Testnet only.
contract BridgeSafeController {
    // -----------------------------------------------------------------------
    // FCC operation identifiers — must match extension/internal/config/config.go
    // -----------------------------------------------------------------------

    /// @notice Operation group for all BridgeSafe treasury actions.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_TYPE_TREASURY = bytes32("TREASURY");

    /// @notice Generate an XRPL keypair inside the enclave and cache the initial policy.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_CMD_CREATE_TREASURY_KEY = bytes32("CREATE_TREASURY_KEY");

    /// @notice Replace the policy the enclave enforces for a treasury.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_CMD_REGISTER_POLICY = bytes32("REGISTER_POLICY");

    /// @notice Decrypt a payment instruction and check it against the policy.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_CMD_AUTHORIZE_PAYMENT = bytes32("AUTHORIZE_PAYMENT");

    /// @notice Sign the canonical XRPL Payment for an already-authorized request.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_CMD_SIGN_XRPL_PAYMENT = bytes32("SIGN_XRPL_PAYMENT");

    // -----------------------------------------------------------------------
    // Domain separation
    // -----------------------------------------------------------------------

    /// @dev Prefix of the 36-byte XRPL memo every BridgeSafe payment must carry:
    ///      `"BSF1" || memoRef`. The prefix keeps the memo legible in an explorer and
    ///      separates BridgeSafe memos from unrelated traffic on the same account.
    // Exactly 4 bytes of ASCII, so the cast cannot truncate.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes4 public constant MEMO_PREFIX = bytes4("BSF1");

    /// @dev Domain tag mixed into `memoRef` so a reference cannot be replayed across
    ///      chains, deployments, or treasuries.
    bytes32 private constant MEMO_DOMAIN = keccak256("BridgeSafe.memoRef.v1");

    /// @dev First extension id the registry assigns to non-system extensions.
    uint256 private constant FIRST_PUBLIC_EXTENSION_ID = 0x10000;

    /// @dev Extra time beyond `expiresAt` before a request that already carries a
    ///      signature may be expired. An XRPL transaction signed by the enclave sets
    ///      `LastLedgerSequence` from `expiresAt`, so once this grace period has passed
    ///      the transaction can no longer be included and the reserved budget is safe to
    ///      release.
    uint64 public constant SETTLEMENT_GRACE = 1 hours;

    /// @dev Guards against a policy so long that a request can never expire.
    uint64 public constant MAX_REQUEST_TTL = 7 days;

    // -----------------------------------------------------------------------
    // Types
    // -----------------------------------------------------------------------

    /// @notice Lifecycle of a payment request.
    enum RequestState {
        NONE, // 0 — never created
        CREATED, // 1 — on chain, AUTHORIZE_PAYMENT instruction dispatched
        AUTHORIZED, // 2 — enclave approved against policy; budget reserved; terms revealed
        SIGNED, // 3 — enclave produced a signed XRPL Payment; expected tx id known
        BROADCAST, // 4 — relayer reported submission to XRPL
        SETTLED, // 5 — FDC proved the payment landed as specified
        EXPIRED, // 6 — deadline passed; budget released
        CANCELLED, // 7 — withdrawn by the treasury owner before signing
        FAILED // 8 — enclave declined, or settlement proved impossible
    }

    /// @notice Spending rules enforced inside the enclave and mirrored here for audit.
    /// @param maxPerPaymentDrops Largest single payment, in XRP drops.
    /// @param maxTotalDrops Lifetime cumulative spend cap, in XRP drops.
    /// @param requestTtlSeconds How long a request stays actionable.
    struct Policy {
        uint256 maxPerPaymentDrops;
        uint256 maxTotalDrops;
        uint64 requestTtlSeconds;
    }

    /// @notice A Flare-controlled XRPL treasury.
    /// @param owner Only address allowed to open payment requests.
    /// @param xrplAddress Classic r-address generated inside the enclave.
    /// @param xrplAddressHash `keccak256(bytes(xrplAddress))` — the form FDC reports.
    /// @param policy Current spending rules.
    /// @param policyCommitment `keccak256(abi.encode(policy))` acknowledged by the enclave.
    /// @param pendingPolicyCommitment Commitment of a policy update awaiting enclave
    ///        acknowledgement, or zero when no update is outstanding. Consumed by
    ///        `confirmPolicy`, which is what stops an old acknowledgement being replayed.
    /// @param reservedDrops Cumulative drops reserved by authorized-or-later requests.
    /// @param nextNonce Next sequential request nonce for this treasury.
    /// @param bound True once the enclave has returned the XRPL address.
    /// @param paused Owner-controlled kill switch for new requests.
    /// @param exists True for any created treasury.
    struct Treasury {
        address owner;
        string xrplAddress;
        bytes32 xrplAddressHash;
        Policy policy;
        bytes32 policyCommitment;
        bytes32 pendingPolicyCommitment;
        uint256 reservedDrops;
        uint64 nextNonce;
        bool bound;
        bool paused;
        bool exists;
    }

    /// @notice A single confidential payment request.
    /// @param treasuryId Owning treasury.
    /// @param requester Address that opened the request.
    /// @param nonce Sequential per-treasury nonce.
    /// @param createdAt Block timestamp at creation.
    /// @param expiresAt Hard deadline derived from the policy TTL.
    /// @param payloadHash `keccak256` of the ECIES ciphertext handed to the enclave.
    /// @param memoRef Reference the XRPL memo must carry. Binds payment to request.
    /// @param amountDrops Payment amount, revealed by the enclave at authorization.
    /// @param destinationHash `keccak256` of the destination r-address, revealed at
    ///        authorization. The plaintext destination stays off chain until settlement
    ///        makes it public on XRPL anyway.
    /// @param expectedTxId XRPL transaction id the enclave computed for its signed blob.
    /// @param signedBlobHash `keccak256` of the signed transaction blob.
    /// @param xrplTxId Transaction id actually observed on XRPL.
    /// @param state Current lifecycle state.
    struct PaymentRequest {
        uint256 treasuryId;
        address requester;
        uint64 nonce;
        uint64 createdAt;
        uint64 expiresAt;
        bytes32 payloadHash;
        bytes32 memoRef;
        uint256 amountDrops;
        bytes32 destinationHash;
        bytes32 expectedTxId;
        bytes32 signedBlobHash;
        bytes32 xrplTxId;
        RequestState state;
    }

    /// @dev Header prepended to every instruction payload. Because only this contract can
    ///      send instructions for the extension, the enclave may treat these fields as
    ///      authenticated without any further proof.
    struct InstructionHeader {
        uint256 chainId;
        address controller;
        uint256 treasuryId;
        uint256 requestId;
        uint64 nonce;
        uint64 expiresAt;
        bytes32 memoRef;
        bytes32 policyCommitment;
    }

    // -----------------------------------------------------------------------
    // Storage
    // -----------------------------------------------------------------------

    /// @notice Contract owner: sets the TEE address and the FDC verifier.
    address public owner;

    /// @notice The FlareTeeManager diamond, as the extension registry.
    ITeeExtensionRegistry public immutable TEE_EXTENSION_REGISTRY;

    /// @notice The FlareTeeManager diamond, as the machine registry.
    ITeeMachineRegistry public immutable TEE_MACHINE_REGISTRY;

    /// @notice Enclave signing address whose results this contract accepts.
    address public teeAddress;

    /// @notice The only contract permitted to move a request to `SETTLED`.
    address public fdcVerifier;

    /// @notice Global kill switch: blocks new treasuries and new requests.
    bool public paused;

    uint256 private _extensionId;

    /// @notice Treasuries by id, starting at 1.
    mapping(uint256 => Treasury) private _treasuries;
    uint256 public treasuryCount;

    /// @notice Payment requests by id, starting at 1.
    mapping(uint256 => PaymentRequest) private _requests;
    uint256 public requestCount;

    /// @notice Requests opened against a treasury, for UI enumeration.
    mapping(uint256 => uint256[]) private _treasuryRequests;

    // -----------------------------------------------------------------------
    // Events
    // -----------------------------------------------------------------------

    event OwnerTransferred(address indexed previousOwner, address indexed newOwner);
    event TeeAddressSet(address indexed teeAddress);
    event FdcVerifierSet(address indexed verifier);
    event ExtensionIdSet(uint256 indexed extensionId);
    event PausedSet(bool paused);
    event TreasuryPausedSet(uint256 indexed treasuryId, bool paused);

    event TreasuryCreated(
        uint256 indexed treasuryId,
        address indexed owner,
        bytes32 policyCommitment,
        bytes32 instructionId
    );
    event TreasuryBound(uint256 indexed treasuryId, string xrplAddress, bytes32 xrplAddressHash);
    event PolicyUpdateRequested(uint256 indexed treasuryId, bytes32 policyCommitment, bytes32 instructionId);
    event PolicyUpdated(uint256 indexed treasuryId, bytes32 policyCommitment);

    event PaymentRequested(
        uint256 indexed requestId,
        uint256 indexed treasuryId,
        address indexed requester,
        uint64 nonce,
        bytes32 memoRef,
        uint64 expiresAt,
        bytes32 payloadHash,
        bytes32 instructionId
    );
    event PaymentAuthorized(
        uint256 indexed requestId,
        uint256 amountDrops,
        bytes32 destinationHash
    );
    event SignatureRequested(uint256 indexed requestId, bytes32 instructionId);
    event PaymentSigned(
        uint256 indexed requestId,
        bytes32 expectedTxId,
        bytes32 signedBlobHash,
        bytes signedTxBlob
    );
    event PaymentBroadcast(uint256 indexed requestId, bytes32 xrplTxId);
    event PaymentSettled(uint256 indexed requestId, bytes32 xrplTxId, uint256 amountDrops);
    event PaymentExpired(uint256 indexed requestId);
    event PaymentCancelled(uint256 indexed requestId);
    event PaymentFailed(uint256 indexed requestId, string reason);

    // -----------------------------------------------------------------------
    // Errors
    // -----------------------------------------------------------------------

    error NotOwner();
    error NotTreasuryOwner();
    error NotFdcVerifier();
    error ContractPaused();
    error TreasuryPaused();
    error ZeroAddress();
    error NoCode();
    error UnknownTreasury(uint256 treasuryId);
    error UnknownRequest(uint256 requestId);
    error TreasuryNotBound(uint256 treasuryId);
    error TreasuryAlreadyBound(uint256 treasuryId);
    error InvalidPolicy(string reason);
    error NoPendingPolicy();
    error WrongState(uint256 requestId, RequestState expected, RequestState actual);
    error RequestExpired(uint256 requestId, uint64 expiresAt);
    error NotYetExpired(uint256 requestId, uint64 expirableAt);
    error TeeAddressUnset();
    error TeeReportedFailure(uint8 status);
    error BadTeeSignature(address recovered);
    error ResultBindingMismatch();
    error PolicyViolation(string reason);
    error EmptyPayload();
    error ExtensionIdAlreadySet();
    error ExtensionIdNotFound();
    error ExtensionIdUnset();
    error TxIdMismatch(bytes32 expected, bytes32 actual);

    // -----------------------------------------------------------------------
    // Modifiers
    // -----------------------------------------------------------------------

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    modifier whenNotPaused() {
        if (paused) revert ContractPaused();
        _;
    }

    // -----------------------------------------------------------------------
    // Construction and administration
    // -----------------------------------------------------------------------

    /// @param _teeExtensionRegistry FlareTeeManager diamond address.
    /// @param _teeMachineRegistry FlareTeeManager diamond address (same diamond).
    constructor(ITeeExtensionRegistry _teeExtensionRegistry, ITeeMachineRegistry _teeMachineRegistry) {
        if (address(_teeExtensionRegistry) == address(0) || address(_teeMachineRegistry) == address(0)) {
            revert ZeroAddress();
        }
        if (address(_teeExtensionRegistry).code.length == 0 || address(_teeMachineRegistry).code.length == 0) {
            revert NoCode();
        }
        TEE_EXTENSION_REGISTRY = _teeExtensionRegistry;
        TEE_MACHINE_REGISTRY = _teeMachineRegistry;
        owner = msg.sender;
        emit OwnerTransferred(address(0), msg.sender);
    }

    /// @notice Discover and cache this contract's extension id. Settable once.
    /// @dev Mirrors the pattern in Flare's FCC examples; the registry is scanned for the
    ///      extension whose InstructionSender is this contract.
    function setExtensionId() external {
        if (_extensionId != 0) revert ExtensionIdAlreadySet();
        uint256 c = TEE_EXTENSION_REGISTRY.nextPublicExtensionId();
        for (uint256 i = FIRST_PUBLIC_EXTENSION_ID; i < c; ++i) {
            if (TEE_EXTENSION_REGISTRY.getTeeExtensionInstructionsSender(i) == address(this)) {
                _extensionId = i;
                emit ExtensionIdSet(i);
                return;
            }
        }
        revert ExtensionIdNotFound();
    }

    /// @notice The cached extension id, or zero if `setExtensionId` has not run.
    function extensionId() external view returns (uint256) {
        return _extensionId;
    }

    /// @notice Register the enclave signing address whose results are accepted.
    function setTeeAddress(address _teeAddress) external onlyOwner {
        if (_teeAddress == address(0)) revert ZeroAddress();
        teeAddress = _teeAddress;
        emit TeeAddressSet(_teeAddress);
    }

    /// @notice Register the only contract allowed to settle requests.
    function setFdcVerifier(address _verifier) external onlyOwner {
        if (_verifier == address(0)) revert ZeroAddress();
        fdcVerifier = _verifier;
        emit FdcVerifierSet(_verifier);
    }

    /// @notice Transfer contract ownership.
    function transferOwnership(address _newOwner) external onlyOwner {
        if (_newOwner == address(0)) revert ZeroAddress();
        emit OwnerTransferred(owner, _newOwner);
        owner = _newOwner;
    }

    /// @notice Global emergency stop for new treasuries and new requests.
    /// @dev Deliberately does not block settlement: an XRPL payment that already exists
    ///      should still be provable on Flare, otherwise pausing would strand requests in
    ///      an unverifiable state.
    function setPaused(bool _paused) external onlyOwner {
        paused = _paused;
        emit PausedSet(_paused);
    }

    // -----------------------------------------------------------------------
    // Treasury lifecycle
    // -----------------------------------------------------------------------

    /// @notice Create a treasury and ask the enclave to generate its XRPL key.
    /// @dev The instruction carries the initial policy, so one round trip both creates
    ///      the key and primes the enclave's policy cache. Forward `msg.value` to cover
    ///      the registry's per-instruction fee.
    /// @param _policy Initial spending rules.
    /// @return treasuryId Id of the new treasury.
    function createTreasury(
        Policy calldata _policy
    ) external payable whenNotPaused returns (uint256 treasuryId) {
        _validatePolicy(_policy);

        treasuryId = ++treasuryCount;
        Treasury storage t = _treasuries[treasuryId];
        t.owner = msg.sender;
        t.policy = _policy;
        t.policyCommitment = policyCommitment(_policy);
        t.nextNonce = 1;
        t.exists = true;

        bytes32 instructionId = _send(
            OP_CMD_CREATE_TREASURY_KEY,
            abi.encode(_header(treasuryId, 0, 0, 0, bytes32(0), t.policyCommitment), _policy)
        );

        emit TreasuryCreated(treasuryId, msg.sender, t.policyCommitment, instructionId);
    }

    /// @notice Bind the XRPL address the enclave generated for a treasury.
    /// @dev Accepts the enclave's signed `ActionResult`. The result data must name this
    ///      contract and this treasury, so a result produced for another deployment
    ///      cannot be replayed here.
    /// @param _resultData ABI-encoded `(uint256 chainId, address controller,
    ///        uint256 treasuryId, string xrplAddress, bytes32 policyCommitment)`.
    function bindTreasuryAddress(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) external {
        _requireTeeResult(_resultData, _actionId, _submissionTag, _status, _signature);

        (
            uint256 chainId_,
            address controller_,
            uint256 treasuryId_,
            string memory xrplAddress_,
            bytes32 policyCommitment_
        ) = abi.decode(_resultData, (uint256, address, uint256, string, bytes32));

        if (chainId_ != block.chainid || controller_ != address(this)) revert ResultBindingMismatch();

        Treasury storage t = _mutTreasury(treasuryId_);
        if (t.bound) revert TreasuryAlreadyBound(treasuryId_);
        if (policyCommitment_ != t.policyCommitment) revert ResultBindingMismatch();
        if (bytes(xrplAddress_).length == 0) revert ResultBindingMismatch();

        t.xrplAddress = xrplAddress_;
        t.xrplAddressHash = keccak256(bytes(xrplAddress_));
        t.bound = true;

        emit TreasuryBound(treasuryId_, xrplAddress_, t.xrplAddressHash);
    }

    /// @notice Ask the enclave to adopt a new policy for a treasury.
    /// @dev The new policy takes effect on chain only once the enclave acknowledges it
    ///      via `confirmPolicy`, so the contract and the enclave can never disagree about
    ///      which rules are live.
    ///
    ///      The commitment is recorded as *pending* here. That record is what
    ///      `confirmPolicy` spends, and it is the reason an acknowledgement cannot be
    ///      replayed: see the note on that function.
    function requestPolicyUpdate(
        uint256 _treasuryId,
        Policy calldata _policy
    ) external payable whenNotPaused {
        Treasury storage t = _mutTreasury(_treasuryId);
        if (msg.sender != t.owner) revert NotTreasuryOwner();
        if (!t.bound) revert TreasuryNotBound(_treasuryId);
        _validatePolicy(_policy);

        // A cap below what is already reserved would leave `availableDrops` underwater.
        if (_policy.maxTotalDrops < t.reservedDrops) {
            revert InvalidPolicy("cumulative cap below reserved spend");
        }

        bytes32 newCommitment = policyCommitment(_policy);
        t.pendingPolicyCommitment = newCommitment;

        bytes32 instructionId = _send(
            OP_CMD_REGISTER_POLICY,
            abi.encode(_header(_treasuryId, 0, 0, 0, bytes32(0), newCommitment), _policy)
        );

        emit PolicyUpdateRequested(_treasuryId, newCommitment, instructionId);
    }

    /// @notice Record the enclave's acknowledgement of a new policy.
    /// @dev This function is permissionless and its authority is an enclave signature,
    ///      which never expires. Every other result handler is protected from replay by
    ///      the state machine — a request can only leave `CREATED` once. A policy has no
    ///      such monotonic state, so the guard is explicit: the acknowledgement must match
    ///      the commitment currently pending for this treasury, and consuming it clears
    ///      the slot.
    ///
    ///      Without that, the signed acknowledgement of a superseded policy stays valid
    ///      forever and anyone could replay it to reinstate looser published limits. Worse
    ///      than the bookkeeping: the enclave declines any payment whose header commitment
    ///      disagrees with its own cached policy, so a rolled-back commitment would stop
    ///      the treasury from paying at all until the owner issued another update — which
    ///      the same replay could undo again immediately.
    ///
    ///      It also means the enclave cannot install limits of its own choosing; it can
    ///      only acknowledge terms the treasury owner already published on chain.
    /// @param _resultData ABI-encoded `(uint256 chainId, address controller,
    ///        uint256 treasuryId, Policy policy, bytes32 policyCommitment)`.
    function confirmPolicy(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) external {
        _requireTeeResult(_resultData, _actionId, _submissionTag, _status, _signature);

        (
            uint256 chainId_,
            address controller_,
            uint256 treasuryId_,
            Policy memory policy_,
            bytes32 commitment_
        ) = abi.decode(_resultData, (uint256, address, uint256, Policy, bytes32));

        if (chainId_ != block.chainid || controller_ != address(this)) revert ResultBindingMismatch();
        if (commitment_ != policyCommitment(policy_)) revert ResultBindingMismatch();

        Treasury storage t = _mutTreasury(treasuryId_);
        if (t.pendingPolicyCommitment == bytes32(0) || commitment_ != t.pendingPolicyCommitment) {
            revert NoPendingPolicy();
        }

        t.pendingPolicyCommitment = bytes32(0);
        t.policy = policy_;
        t.policyCommitment = commitment_;

        emit PolicyUpdated(treasuryId_, commitment_);
    }

    /// @notice Pause or resume a single treasury.
    function setTreasuryPaused(uint256 _treasuryId, bool _paused) external {
        Treasury storage t = _mutTreasury(_treasuryId);
        if (msg.sender != t.owner && msg.sender != owner) revert NotTreasuryOwner();
        t.paused = _paused;
        emit TreasuryPausedSet(_treasuryId, _paused);
    }

    // -----------------------------------------------------------------------
    // Payment lifecycle
    // -----------------------------------------------------------------------

    /// @notice Open a confidential payment request.
    /// @dev `_encryptedPayload` is an ECIES ciphertext under the enclave's public key.
    ///      Only its hash is stored; the ciphertext travels in the instruction. Amount
    ///      and destination stay sealed until the enclave authorizes the payment.
    /// @param _treasuryId Treasury to pay from.
    /// @param _encryptedPayload ECIES ciphertext of the ABI-encoded payment instruction.
    /// @return requestId Id of the new request.
    function createPaymentRequest(
        uint256 _treasuryId,
        bytes calldata _encryptedPayload
    ) external payable whenNotPaused returns (uint256 requestId) {
        if (_encryptedPayload.length == 0) revert EmptyPayload();

        Treasury storage t = _mutTreasury(_treasuryId);
        if (msg.sender != t.owner) revert NotTreasuryOwner();
        if (!t.bound) revert TreasuryNotBound(_treasuryId);
        if (t.paused) revert TreasuryPaused();

        requestId = ++requestCount;
        uint64 nonce = t.nextNonce++;
        uint64 expiresAt = uint64(block.timestamp) + t.policy.requestTtlSeconds;
        bytes32 ref = memoRef(_treasuryId, requestId, nonce);

        PaymentRequest storage r = _requests[requestId];
        r.treasuryId = _treasuryId;
        r.requester = msg.sender;
        r.nonce = nonce;
        r.createdAt = uint64(block.timestamp);
        r.expiresAt = expiresAt;
        r.payloadHash = keccak256(_encryptedPayload);
        r.memoRef = ref;
        r.state = RequestState.CREATED;

        _treasuryRequests[_treasuryId].push(requestId);

        bytes32 instructionId = _send(
            OP_CMD_AUTHORIZE_PAYMENT,
            abi.encode(
                _header(_treasuryId, requestId, nonce, expiresAt, ref, t.policyCommitment),
                _encryptedPayload
            )
        );

        emit PaymentRequested(
            requestId,
            _treasuryId,
            msg.sender,
            nonce,
            ref,
            expiresAt,
            r.payloadHash,
            instructionId
        );
    }

    /// @notice Record the enclave's authorization decision for a request.
    /// @dev Reserves the amount against the treasury's cumulative cap *before* any
    ///      signature exists. That ordering is what stops two concurrent requests from
    ///      both being signed against the same remaining budget.
    /// @param _resultData ABI-encoded `(uint256 chainId, address controller,
    ///        uint256 requestId, bytes32 memoRef, uint256 amountDrops,
    ///        bytes32 destinationHash, bytes32 payloadHash)`.
    function submitAuthorization(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) external {
        _requireTeeResult(_resultData, _actionId, _submissionTag, _status, _signature);

        (
            uint256 chainId_,
            address controller_,
            uint256 requestId_,
            bytes32 memoRef_,
            uint256 amountDrops_,
            bytes32 destinationHash_,
            bytes32 payloadHash_
        ) = abi.decode(_resultData, (uint256, address, uint256, bytes32, uint256, bytes32, bytes32));

        if (chainId_ != block.chainid || controller_ != address(this)) revert ResultBindingMismatch();

        PaymentRequest storage r = _mutRequest(requestId_);
        _requireState(requestId_, r.state, RequestState.CREATED);
        if (block.timestamp > r.expiresAt) revert RequestExpired(requestId_, r.expiresAt);

        // The enclave must be answering *this* request, about *this* ciphertext.
        if (memoRef_ != r.memoRef || payloadHash_ != r.payloadHash) revert ResultBindingMismatch();
        if (destinationHash_ == bytes32(0) || amountDrops_ == 0) revert PolicyViolation("empty terms");

        Treasury storage t = _treasuries[r.treasuryId];

        // Re-check the policy on chain. The enclave already enforced it, but a public,
        // independent check means a compromised enclave still cannot exceed the published
        // limits without the violation being visible here.
        if (amountDrops_ > t.policy.maxPerPaymentDrops) revert PolicyViolation("exceeds per-payment cap");
        uint256 newReserved = t.reservedDrops + amountDrops_;
        if (newReserved > t.policy.maxTotalDrops) revert PolicyViolation("exceeds cumulative cap");

        t.reservedDrops = newReserved;
        r.amountDrops = amountDrops_;
        r.destinationHash = destinationHash_;
        r.state = RequestState.AUTHORIZED;

        emit PaymentAuthorized(requestId_, amountDrops_, destinationHash_);
    }

    /// @notice Ask the enclave to sign the XRPL payment for an authorized request.
    function requestSignature(uint256 _requestId) external payable whenNotPaused {
        PaymentRequest storage r = _mutRequest(_requestId);
        Treasury storage t = _treasuries[r.treasuryId];
        if (msg.sender != t.owner) revert NotTreasuryOwner();
        if (t.paused) revert TreasuryPaused();
        _requireState(_requestId, r.state, RequestState.AUTHORIZED);
        if (block.timestamp > r.expiresAt) revert RequestExpired(_requestId, r.expiresAt);

        bytes32 instructionId = _send(
            OP_CMD_SIGN_XRPL_PAYMENT,
            abi.encode(_header(r.treasuryId, _requestId, r.nonce, r.expiresAt, r.memoRef, t.policyCommitment))
        );

        emit SignatureRequested(_requestId, instructionId);
    }

    /// @notice Record the signed XRPL payment the enclave produced.
    /// @dev The signed blob travels inside the enclave-signed result and is re-emitted
    ///      here, for two reasons. The stored hash is *derived* from the blob rather than
    ///      asserted alongside it, so no submitter can pair a real signature with a
    ///      different transaction. And publishing the blob means any observer can
    ///      broadcast it, so an uncooperative relayer can delay a payment but cannot
    ///      strand one.
    ///
    ///      Publishing it early is safe: the blob is already signed for one destination
    ///      and one amount, carries this request's memo, and expires with its
    ///      `LastLedgerSequence`. There is nothing an observer can do with it that the
    ///      treasury owner did not already authorize.
    /// @param _resultData ABI-encoded `(uint256 chainId, address controller,
    ///        uint256 requestId, bytes32 memoRef, bytes32 expectedTxId,
    ///        bytes signedTxBlob)`.
    function submitSignedPayment(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) external {
        _requireTeeResult(_resultData, _actionId, _submissionTag, _status, _signature);

        (
            uint256 chainId_,
            address controller_,
            uint256 requestId_,
            bytes32 memoRef_,
            bytes32 expectedTxId_,
            bytes memory signedTxBlob_
        ) = abi.decode(_resultData, (uint256, address, uint256, bytes32, bytes32, bytes));

        if (chainId_ != block.chainid || controller_ != address(this)) revert ResultBindingMismatch();

        PaymentRequest storage r = _mutRequest(requestId_);
        _requireState(requestId_, r.state, RequestState.AUTHORIZED);
        if (block.timestamp > r.expiresAt) revert RequestExpired(requestId_, r.expiresAt);
        if (memoRef_ != r.memoRef) revert ResultBindingMismatch();
        if (expectedTxId_ == bytes32(0) || signedTxBlob_.length == 0) revert ResultBindingMismatch();

        r.expectedTxId = expectedTxId_;
        r.signedBlobHash = keccak256(signedTxBlob_);
        r.state = RequestState.SIGNED;

        emit PaymentSigned(requestId_, expectedTxId_, r.signedBlobHash, signedTxBlob_);
    }

    /// @notice Report that the signed payment was submitted to XRPL.
    /// @dev Permissionless: the relayer holds no key and this only advances observability.
    ///      The id must equal the one the enclave predicted, which it will, because an
    ///      XRPL transaction id is the hash of the signed blob.
    function reportBroadcast(uint256 _requestId, bytes32 _xrplTxId) external {
        PaymentRequest storage r = _mutRequest(_requestId);
        _requireState(_requestId, r.state, RequestState.SIGNED);
        if (_xrplTxId != r.expectedTxId) revert TxIdMismatch(r.expectedTxId, _xrplTxId);

        r.xrplTxId = _xrplTxId;
        r.state = RequestState.BROADCAST;

        emit PaymentBroadcast(_requestId, _xrplTxId);
    }

    /// @notice Mark a request settled. Callable only by the registered FDC verifier.
    /// @dev The verifier has already checked a Flare Data Connector proof against the
    ///      expected source account, destination, amount, memo reference and success
    ///      status, and has recorded the transaction id as consumed.
    function markSettled(uint256 _requestId, bytes32 _xrplTxId) external {
        if (msg.sender != fdcVerifier) revert NotFdcVerifier();

        PaymentRequest storage r = _mutRequest(_requestId);
        if (r.state != RequestState.SIGNED && r.state != RequestState.BROADCAST) {
            revert WrongState(_requestId, RequestState.BROADCAST, r.state);
        }

        r.xrplTxId = _xrplTxId;
        r.state = RequestState.SETTLED;

        emit PaymentSettled(_requestId, _xrplTxId, r.amountDrops);
    }

    /// @notice Withdraw a request that has not been signed yet.
    function cancelRequest(uint256 _requestId) external {
        PaymentRequest storage r = _mutRequest(_requestId);
        Treasury storage t = _treasuries[r.treasuryId];
        if (msg.sender != t.owner) revert NotTreasuryOwner();
        if (r.state != RequestState.CREATED && r.state != RequestState.AUTHORIZED) {
            revert WrongState(_requestId, RequestState.AUTHORIZED, r.state);
        }

        if (r.state == RequestState.AUTHORIZED) t.reservedDrops -= r.amountDrops;
        r.state = RequestState.CANCELLED;

        emit PaymentCancelled(_requestId);
    }

    /// @notice Expire a stale request and release its reserved budget.
    /// @dev Before a signature exists, `expiresAt` is enough. Afterwards the enclave's
    ///      `LastLedgerSequence` still has to lapse on XRPL, so `SETTLEMENT_GRACE` is
    ///      added — releasing the budget any earlier could let a still-includable payment
    ///      and a fresh request share the same allowance.
    function expireRequest(uint256 _requestId) external {
        PaymentRequest storage r = _mutRequest(_requestId);
        uint64 expirableAt = r.expiresAt;

        if (r.state == RequestState.SIGNED || r.state == RequestState.BROADCAST) {
            expirableAt = r.expiresAt + SETTLEMENT_GRACE;
        } else if (r.state != RequestState.CREATED && r.state != RequestState.AUTHORIZED) {
            revert WrongState(_requestId, RequestState.CREATED, r.state);
        }

        if (block.timestamp <= expirableAt) revert NotYetExpired(_requestId, expirableAt);

        if (r.state != RequestState.CREATED) {
            _treasuries[r.treasuryId].reservedDrops -= r.amountDrops;
        }
        r.state = RequestState.EXPIRED;

        emit PaymentExpired(_requestId);
    }

    /// @notice Record an enclave refusal, releasing any reserved budget.
    /// @dev Used when the enclave answers with `status == 0`. The signature is still
    ///      verified, so a third party cannot mark a healthy request failed.
    /// @param _resultData ABI-encoded `(uint256 chainId, address controller,
    ///        uint256 requestId, string reason)`.
    function submitFailure(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) external {
        if (teeAddress == address(0)) revert TeeAddressUnset();
        // A failure report is the one case where status 0 is the expected value.
        if (_status != 0) revert TeeReportedFailure(_status);
        address signer = TeeResult.recoverSigner(_resultData, _actionId, _submissionTag, _status, _signature);
        if (signer != teeAddress) revert BadTeeSignature(signer);

        (uint256 chainId_, address controller_, uint256 requestId_, string memory reason_) = abi.decode(
            _resultData,
            (uint256, address, uint256, string)
        );
        if (chainId_ != block.chainid || controller_ != address(this)) revert ResultBindingMismatch();

        PaymentRequest storage r = _mutRequest(requestId_);
        if (r.state != RequestState.CREATED && r.state != RequestState.AUTHORIZED) {
            revert WrongState(requestId_, RequestState.CREATED, r.state);
        }

        if (r.state == RequestState.AUTHORIZED) {
            _treasuries[r.treasuryId].reservedDrops -= r.amountDrops;
        }
        r.state = RequestState.FAILED;

        emit PaymentFailed(requestId_, reason_);
    }

    // -----------------------------------------------------------------------
    // Views
    // -----------------------------------------------------------------------

    /// @notice Full treasury record.
    function getTreasury(uint256 _treasuryId) external view returns (Treasury memory) {
        Treasury storage t = _treasuries[_treasuryId];
        if (!t.exists) revert UnknownTreasury(_treasuryId);
        return t;
    }

    /// @notice Full payment request record.
    function getRequest(uint256 _requestId) external view returns (PaymentRequest memory) {
        PaymentRequest storage r = _requests[_requestId];
        if (r.state == RequestState.NONE) revert UnknownRequest(_requestId);
        return r;
    }

    /// @notice Ids of every request opened against a treasury, oldest first.
    function getTreasuryRequests(uint256 _treasuryId) external view returns (uint256[] memory) {
        return _treasuryRequests[_treasuryId];
    }

    /// @notice Values `BridgeSafeFdcVerifier` matches an FDC proof against.
    /// @return sourceAddressHash Expected treasury r-address hash.
    /// @return destinationHash Expected destination r-address hash.
    /// @return amountDrops Expected received amount in drops.
    /// @return expectedMemo Exact bytes the XRPL memo must contain.
    /// @return state Current request state.
    function settlementExpectation(
        uint256 _requestId
    )
        external
        view
        returns (
            bytes32 sourceAddressHash,
            bytes32 destinationHash,
            uint256 amountDrops,
            bytes memory expectedMemo,
            RequestState state
        )
    {
        PaymentRequest storage r = _requests[_requestId];
        if (r.state == RequestState.NONE) revert UnknownRequest(_requestId);
        Treasury storage t = _treasuries[r.treasuryId];
        return (
            t.xrplAddressHash,
            r.destinationHash,
            r.amountDrops,
            encodeMemo(r.memoRef),
            r.state
        );
    }

    /// @notice Remaining spend allowance for a treasury, in drops.
    function availableDrops(uint256 _treasuryId) external view returns (uint256) {
        Treasury storage t = _treasuries[_treasuryId];
        if (!t.exists) revert UnknownTreasury(_treasuryId);
        return t.policy.maxTotalDrops - t.reservedDrops;
    }

    /// @notice Commitment the enclave acknowledges for a policy.
    function policyCommitment(Policy memory _policy) public pure returns (bytes32) {
        return keccak256(abi.encode(_policy));
    }

    /// @notice Reference a request's XRPL payment memo must carry.
    function memoRef(uint256 _treasuryId, uint256 _requestId, uint64 _nonce) public view returns (bytes32) {
        return keccak256(abi.encode(MEMO_DOMAIN, block.chainid, address(this), _treasuryId, _requestId, _nonce));
    }

    /// @notice Exact 36 memo bytes for a reference: `"BSF1" || memoRef`.
    function encodeMemo(bytes32 _memoRef) public pure returns (bytes memory) {
        return abi.encodePacked(MEMO_PREFIX, _memoRef);
    }

    // -----------------------------------------------------------------------
    // Internals
    // -----------------------------------------------------------------------

    /// @dev Route an instruction to one TEE machine serving this extension.
    function _send(bytes32 _command, bytes memory _message) private returns (bytes32) {
        if (_extensionId == 0) revert ExtensionIdUnset();

        address[] memory teeIds = TEE_MACHINE_REGISTRY.getRandomTeeIds(_extensionId, 1);
        address[] memory cosigners = new address[](0);

        ITeeExtensionRegistry.TeeInstructionParams memory params = ITeeExtensionRegistry.TeeInstructionParams({
            opType: OP_TYPE_TREASURY,
            opCommand: _command,
            message: _message,
            cosigners: cosigners,
            cosignersThreshold: 0,
            claimBackAddress: msg.sender
        });

        return TEE_EXTENSION_REGISTRY.sendInstructions{value: msg.value}(teeIds, params);
    }

    /// @dev Build the authenticated header prepended to every instruction payload.
    function _header(
        uint256 _treasuryId,
        uint256 _requestId,
        uint64 _nonce,
        uint64 _expiresAt,
        bytes32 _memoRef,
        bytes32 _policyCommitment
    ) private view returns (InstructionHeader memory) {
        return
            InstructionHeader({
                chainId: block.chainid,
                controller: address(this),
                treasuryId: _treasuryId,
                requestId: _requestId,
                nonce: _nonce,
                expiresAt: _expiresAt,
                memoRef: _memoRef,
                policyCommitment: _policyCommitment
            });
    }

    /// @dev Verify a successful, correctly signed enclave result.
    function _requireTeeResult(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) private view {
        if (teeAddress == address(0)) revert TeeAddressUnset();
        if (_status != 1) revert TeeReportedFailure(_status);
        address signer = TeeResult.recoverSigner(_resultData, _actionId, _submissionTag, _status, _signature);
        if (signer != teeAddress) revert BadTeeSignature(signer);
    }

    function _validatePolicy(Policy calldata _policy) private pure {
        if (_policy.maxPerPaymentDrops == 0) revert InvalidPolicy("per-payment cap is zero");
        if (_policy.maxTotalDrops < _policy.maxPerPaymentDrops) revert InvalidPolicy("cumulative cap below per-payment cap");
        if (_policy.requestTtlSeconds == 0) revert InvalidPolicy("ttl is zero");
        if (_policy.requestTtlSeconds > MAX_REQUEST_TTL) revert InvalidPolicy("ttl too long");
    }

    function _mutTreasury(uint256 _treasuryId) private view returns (Treasury storage t) {
        t = _treasuries[_treasuryId];
        if (!t.exists) revert UnknownTreasury(_treasuryId);
    }

    function _mutRequest(uint256 _requestId) private view returns (PaymentRequest storage r) {
        r = _requests[_requestId];
        if (r.state == RequestState.NONE) revert UnknownRequest(_requestId);
    }

    function _requireState(uint256 _requestId, RequestState _actual, RequestState _expected) private pure {
        if (_actual != _expected) revert WrongState(_requestId, _expected, _actual);
    }
}
