// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {Test} from "forge-std/Test.sol";
import {IXRPPayment} from "@flarenetwork/flare-periphery-contracts/coston2/IXRPPayment.sol";

import {BridgeSafeController} from "../src/BridgeSafeController.sol";
import {BridgeSafeFdcVerifier} from "../src/BridgeSafeFdcVerifier.sol";
import {TeeResult} from "../src/lib/TeeResult.sol";
import {MockTeeRegistry} from "./mocks/MockTeeRegistry.sol";
import {MockFdcVerification} from "./mocks/MockFdcVerification.sol";

/// @notice Shared fixture: deploys the system and reproduces the enclave's signing
///         behaviour so tests can mint authentic `ActionResult` signatures.
///
/// @dev The signing helper here is the reference implementation of the same hash the Go
///      extension signs. If these two ever drift, the on-chain checks would silently stop
///      matching real enclave output — so `test_TeeSigningDomain_MatchesSpec` pins the
///      exact byte layout, and `extension/internal/tee/result_test.go` pins the Go side
///      against the same vector.
abstract contract BridgeSafeTestBase is Test {
    // XRPL Testnet source id as FDC reports it.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 internal constant SOURCE_TEST_XRP = bytes32("testXRP");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 internal constant ATTESTATION_XRP_PAYMENT = bytes32("XRPPayment");

    uint256 internal constant DROP = 1;
    uint256 internal constant XRP = 1_000_000; // drops per XRP

    MockTeeRegistry internal registry;
    MockFdcVerification internal fdc;
    BridgeSafeController internal controller;
    BridgeSafeFdcVerifier internal verifier;

    uint256 internal teeKey;
    address internal teeAddr;

    address internal admin = makeAddr("admin");
    address internal treasuryOwner = makeAddr("treasuryOwner");
    address internal stranger = makeAddr("stranger");
    address internal relayer = makeAddr("relayer");

    /// @dev The treasury's own XRPL account, generated inside the enclave in production.
    string internal constant TREASURY_XRPL = "rBridgeSafeTreasuryAccountAddr1";
    string internal constant PAYEE_XRPL = "rContractorPayeeAccountAddress2";

    function setUp() public virtual {
        (teeAddr, teeKey) = makeAddrAndKey("teeEnclave");

        registry = new MockTeeRegistry();
        fdc = new MockFdcVerification();

        vm.prank(admin);
        controller = new BridgeSafeController(registry, registry);

        registry.bindSender(address(controller));
        controller.setExtensionId();

        verifier = new BridgeSafeFdcVerifier(controller, SOURCE_TEST_XRP, address(fdc));

        vm.startPrank(admin);
        controller.setTeeAddress(teeAddr);
        controller.setFdcVerifier(address(verifier));
        vm.stopPrank();

        vm.deal(treasuryOwner, 100 ether);
        vm.deal(stranger, 100 ether);
        vm.deal(relayer, 100 ether);

        // Requests carry wall-clock deadlines; start well clear of zero.
        vm.warp(1_800_000_000);
    }

    // -----------------------------------------------------------------------
    // Enclave signing — mirrors go-flare-common signing.TEEActionResult
    // -----------------------------------------------------------------------

    /// @notice Produce the signature a TEE node would place on an `ActionResult`.
    function signAsTee(
        bytes memory _resultData,
        bytes32 _actionId,
        string memory _tag,
        uint8 _status
    ) internal view returns (bytes memory) {
        return signAsKey(teeKey, _resultData, _actionId, _tag, _status);
    }

    /// @notice Same, under an arbitrary key — used to prove a foreign signer is rejected.
    function signAsKey(
        uint256 _key,
        bytes memory _resultData,
        bytes32 _actionId,
        string memory _tag,
        uint8 _status
    ) internal view returns (bytes memory) {
        bytes32 digest = teeDigest(_resultData, _actionId, _tag, _status);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(_key, digest);
        return abi.encodePacked(r, s, v);
    }

    /// @notice The exact EIP-191 digest the enclave signs.
    function teeDigest(
        bytes memory _resultData,
        bytes32 _actionId,
        string memory _tag,
        uint8 _status
    ) internal view returns (bytes32) {
        bytes32 resultHash = keccak256(
            abi.encodePacked(keccak256(_resultData), _actionId, keccak256(bytes(_tag)), _status)
        );
        // chainid is fixed at 114 (Coston2) in these tests; see setUp of the deriving
        // suite if a test needs to vary it.
        bytes32 payloadHash = keccak256(
            abi.encode(TeeResult.TEE_ACTION_RESULT_PREFIX, block.chainid, resultHash)
        );
        return keccak256(abi.encodePacked("\x19Ethereum Signed Message:\n32", payloadHash));
    }

    // -----------------------------------------------------------------------
    // Fixture builders
    // -----------------------------------------------------------------------

    function defaultPolicy() internal pure returns (BridgeSafeController.Policy memory) {
        return
            BridgeSafeController.Policy({
                maxPerPaymentDrops: 100 * XRP, // 100 test XRP per payment
                maxTotalDrops: 500 * XRP, // 500 test XRP lifetime
                requestTtlSeconds: 30 minutes
            });
    }

    /// @notice Create a treasury and bind its enclave-generated XRPL address.
    function createBoundTreasury() internal returns (uint256 treasuryId) {
        vm.prank(treasuryOwner);
        treasuryId = controller.createTreasury{value: 0.01 ether}(defaultPolicy());

        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            treasuryId,
            TREASURY_XRPL,
            controller.policyCommitment(defaultPolicy())
        );
        bytes32 actionId = keccak256("create-treasury-key");
        controller.bindTreasuryAddress(data, actionId, "tag", 1, signAsTee(data, actionId, "tag", 1));
    }

    /// @notice Open a payment request against a bound treasury.
    function openRequest(uint256 _treasuryId) internal returns (uint256 requestId) {
        vm.prank(treasuryOwner);
        requestId = controller.createPaymentRequest{value: 0.01 ether}(_treasuryId, ciphertext());
    }

    /// @notice Stand-in for the ECIES ciphertext of a payment instruction.
    function ciphertext() internal pure returns (bytes memory) {
        return hex"04a1b2c3d4e5f60718293a4b5c6d7e8f90";
    }

    /// @notice Build a signed authorization without submitting it.
    /// @dev Negative tests need this: `vm.expectRevert` binds to the very next external
    ///      call, so anything that reads contract state must happen before the cheatcode.
    function buildAuthorization(
        uint256 _requestId,
        uint256 _amountDrops,
        string memory _destination
    ) internal view returns (bytes memory data, bytes32 actionId, bytes memory sig) {
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(_requestId);
        data = abi.encode(
            block.chainid,
            address(controller),
            _requestId,
            r.memoRef,
            _amountDrops,
            keccak256(bytes(_destination)),
            r.payloadHash
        );
        actionId = keccak256(abi.encode("authorize", _requestId));
        sig = signAsTee(data, actionId, "tag", 1);
    }

    /// @notice Move a request to AUTHORIZED with an enclave-signed decision.
    function authorize(uint256 _requestId, uint256 _amountDrops, string memory _destination) internal {
        (bytes memory data, bytes32 actionId, bytes memory sig) = buildAuthorization(
            _requestId,
            _amountDrops,
            _destination
        );
        controller.submitAuthorization(data, actionId, "tag", 1, sig);
    }

    /// @notice Move a request to SIGNED with an enclave-signed XRPL payment.
    function sign(uint256 _requestId, bytes32 _expectedTxId) internal {
        vm.prank(treasuryOwner);
        controller.requestSignature{value: 0.01 ether}(_requestId);

        BridgeSafeController.PaymentRequest memory r = controller.getRequest(_requestId);
        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            _requestId,
            r.memoRef,
            _expectedTxId,
            keccak256("signed-blob")
        );
        bytes32 actionId = keccak256(abi.encode("sign", _requestId));
        controller.submitSignedPayment(data, actionId, "tag", 1, signAsTee(data, actionId, "tag", 1));
    }

    /// @notice Drive a request all the way to SIGNED with default terms.
    function requestThroughSigned(
        uint256 _treasuryId,
        uint256 _amountDrops,
        bytes32 _txId
    ) internal returns (uint256 requestId) {
        requestId = openRequest(_treasuryId);
        authorize(requestId, _amountDrops, PAYEE_XRPL);
        sign(requestId, _txId);
    }

    // -----------------------------------------------------------------------
    // FDC proof construction
    // -----------------------------------------------------------------------

    /// @notice Build a well-formed XRPPayment proof matching a request's expectation.
    function goodProof(
        uint256 _requestId,
        bytes32 _txId,
        uint256 _amountDrops
    ) internal view returns (IXRPPayment.Proof memory) {
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(_requestId);
        return
            buildProof(
                _txId,
                keccak256(bytes(TREASURY_XRPL)),
                keccak256(bytes(PAYEE_XRPL)),
                int256(_amountDrops),
                controller.encodeMemo(r.memoRef),
                0
            );
    }

    /// @notice Assemble an arbitrary XRPPayment proof so negative tests can vary any field.
    function buildProof(
        bytes32 _txId,
        bytes32 _sourceHash,
        bytes32 _destHash,
        int256 _receivedDrops,
        bytes memory _memo,
        uint8 _status
    ) internal view returns (IXRPPayment.Proof memory proof) {
        IXRPPayment.ResponseBody memory body = IXRPPayment.ResponseBody({
            blockNumber: 19_432_285,
            blockTimestamp: uint64(block.timestamp),
            sourceAddress: TREASURY_XRPL,
            sourceAddressHash: _sourceHash,
            receivingAddressHash: _destHash,
            intendedReceivingAddressHash: _destHash,
            spentAmount: _receivedDrops + 10,
            intendedSpentAmount: _receivedDrops + 10,
            receivedAmount: _receivedDrops,
            intendedReceivedAmount: _receivedDrops,
            hasMemoData: _memo.length > 0,
            firstMemoData: _memo,
            hasDestinationTag: false,
            destinationTag: 0,
            status: _status
        });

        proof = IXRPPayment.Proof({
            merkleProof: new bytes32[](0),
            data: IXRPPayment.Response({
                attestationType: ATTESTATION_XRP_PAYMENT,
                sourceId: SOURCE_TEST_XRP,
                votingRound: 1234,
                lowestUsedTimestamp: uint64(block.timestamp),
                requestBody: IXRPPayment.RequestBody({
                    transactionId: _txId,
                    proofOwner: address(verifier)
                }),
                responseBody: body
            })
        });
    }

    function stateOf(uint256 _requestId) internal view returns (BridgeSafeController.RequestState) {
        return controller.getRequest(_requestId).state;
    }
}
