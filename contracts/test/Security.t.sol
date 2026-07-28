// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {IXRPPayment} from "@flarenetwork/flare-periphery-contracts/coston2/IXRPPayment.sol";

import {BridgeSafeTestBase} from "./BridgeSafeTestBase.sol";
import {BridgeSafeController} from "../src/BridgeSafeController.sol";
import {BridgeSafeFdcVerifier} from "../src/BridgeSafeFdcVerifier.sol";
import {TeeResult} from "../src/lib/TeeResult.sol";
import {ITeeExtensionRegistry} from "../src/interfaces/ITeeExtensionRegistry.sol";
import {MockTeeRegistry} from "./mocks/MockTeeRegistry.sol";

/// @notice Every way the system is supposed to refuse.
///
/// @dev These are the tests that matter. The happy path proves the product works; this
///      file proves it cannot be talked into paying the wrong person, paying twice, or
///      accepting a settlement that never happened.
contract SecurityTest is BridgeSafeTestBase {
    bytes32 internal constant TX_ID = keccak256("xrpl-tx-1");

    // -----------------------------------------------------------------------
    // Settlement proof: the FDC checks
    // -----------------------------------------------------------------------

    function test_Reject_ReusedXrplTransactionProof() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 r1 = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);
        verifier.finalizePayment(r1, goodProof(r1, TX_ID, 10 * XRP));

        // A second request, then an attempt to settle it with the *same* XRPL payment.
        uint256 r2 = requestThroughSigned(treasuryId, 10 * XRP, keccak256("xrpl-tx-2"));

        BridgeSafeController.PaymentRequest memory req2 = controller.getRequest(r2);
        IXRPPayment.Proof memory replay = buildProof(
            TX_ID,
            keccak256(bytes(TREASURY_XRPL)),
            keccak256(bytes(PAYEE_XRPL)),
            int256(10 * XRP),
            controller.encodeMemo(req2.memoRef),
            0
        );

        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeFdcVerifier.TxIdAlreadyUsed.selector, TX_ID, r1)
        );
        verifier.finalizePayment(r2, replay);
    }

    function test_Reject_SettlingSameRequestTwice() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);
        IXRPPayment.Proof memory p = goodProof(requestId, TX_ID, 10 * XRP);
        verifier.finalizePayment(requestId, p);

        // The transaction-id guard is checked before the request state, so replaying the
        // identical proof trips that first. Both guards are independently sufficient;
        // test_Reject_SettledRequestRejectsFreshProof covers the state one on its own.
        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeFdcVerifier.TxIdAlreadyUsed.selector, TX_ID, requestId)
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_SettledRequestRejectsFreshProof() public {
        // A settled request must not be settleable again even by a *different*, otherwise
        // valid XRPL payment — so the state guard has to hold without help from the
        // transaction-id guard.
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);
        verifier.finalizePayment(requestId, goodProof(requestId, TX_ID, 10 * XRP));

        IXRPPayment.Proof memory second = goodProof(requestId, keccak256("a-second-payment"), 10 * XRP);
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.RequestNotBroadcastable.selector,
                requestId,
                BridgeSafeController.RequestState.SETTLED
            )
        );
        verifier.finalizePayment(requestId, second);
    }

    function test_Reject_InvalidMerkleProof() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        IXRPPayment.Proof memory p = goodProof(requestId, TX_ID, 10 * XRP);

        fdc.setAccept(false);
        vm.expectRevert(BridgeSafeFdcVerifier.InvalidFdcProof.selector);
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_WrongAmount() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 25 * XRP, TX_ID);

        // One drop short of the authorized amount.
        IXRPPayment.Proof memory p = goodProof(requestId, TX_ID, 25 * XRP - 1);
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.AmountMismatch.selector,
                25 * XRP,
                int256(25 * XRP - 1)
            )
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_WrongRecipient() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes32 attacker = keccak256(bytes("rAttackerAccountAddressHere0001"));
        IXRPPayment.Proof memory p = buildProof(
            TX_ID,
            keccak256(bytes(TREASURY_XRPL)),
            attacker,
            int256(10 * XRP),
            controller.encodeMemo(r.memoRef),
            0
        );

        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.DestinationMismatch.selector,
                keccak256(bytes(PAYEE_XRPL)),
                attacker
            )
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_WrongSourceAccount() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        // Correct payment, correct memo — but sent from someone else's account. Without
        // this check a third party could fund the payee and claim the treasury did it.
        bytes32 foreign = keccak256(bytes("rSomeoneElsesAccountAddress0001"));
        IXRPPayment.Proof memory p = buildProof(
            foreign,
            foreign,
            keccak256(bytes(PAYEE_XRPL)),
            int256(10 * XRP),
            controller.encodeMemo(r.memoRef),
            0
        );

        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.SourceAccountMismatch.selector,
                keccak256(bytes(TREASURY_XRPL)),
                foreign
            )
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_MissingMemo() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        IXRPPayment.Proof memory p = buildProof(
            TX_ID,
            keccak256(bytes(TREASURY_XRPL)),
            keccak256(bytes(PAYEE_XRPL)),
            int256(10 * XRP),
            "",
            0
        );

        vm.expectRevert(BridgeSafeFdcVerifier.MissingMemo.selector);
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_MemoFromAnotherRequest() public {
        // The treasury pays the same payee the same amount twice. Only the memo
        // distinguishes the two payments, so this is the case where a weak binding would
        // let one XRPL payment satisfy the wrong request.
        uint256 treasuryId = createBoundTreasury();
        uint256 r1 = openRequest(treasuryId);
        authorize(r1, 10 * XRP, PAYEE_XRPL);
        uint256 r2 = openRequest(treasuryId);
        authorize(r2, 10 * XRP, PAYEE_XRPL);
        sign(r2, TX_ID);

        bytes memory wrongMemo = controller.encodeMemo(controller.getRequest(r1).memoRef);
        IXRPPayment.Proof memory p = buildProof(
            TX_ID,
            keccak256(bytes(TREASURY_XRPL)),
            keccak256(bytes(PAYEE_XRPL)),
            int256(10 * XRP),
            wrongMemo,
            0
        );

        vm.expectRevert();
        verifier.finalizePayment(r2, p);
    }

    function test_Reject_FailedXrplTransaction() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        IXRPPayment.Proof memory p = buildProof(
            TX_ID,
            keccak256(bytes(TREASURY_XRPL)),
            keccak256(bytes(PAYEE_XRPL)),
            int256(10 * XRP),
            controller.encodeMemo(r.memoRef),
            2 // RECEIVER_FAILURE
        );

        vm.expectRevert(abi.encodeWithSelector(BridgeSafeFdcVerifier.XrplTransactionFailed.selector, uint8(2)));
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_ProofFromWrongSourceChain() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        IXRPPayment.Proof memory p = goodProof(requestId, TX_ID, 10 * XRP);
        p.data.sourceId = bytes32("XRP"); // mainnet instead of testXRP

        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.WrongSourceId.selector,
                SOURCE_TEST_XRP,
                bytes32("XRP")
            )
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_WrongAttestationType() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        IXRPPayment.Proof memory p = goodProof(requestId, TX_ID, 10 * XRP);
        p.data.attestationType = bytes32("Payment");

        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.WrongAttestationType.selector,
                ATTESTATION_XRP_PAYMENT,
                bytes32("Payment")
            )
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_SettlingRequestThatWasNeverSigned() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        authorize(requestId, 10 * XRP, PAYEE_XRPL);
        IXRPPayment.Proof memory p = goodProof(requestId, TX_ID, 10 * XRP);

        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeFdcVerifier.RequestNotBroadcastable.selector,
                requestId,
                BridgeSafeController.RequestState.AUTHORIZED
            )
        );
        verifier.finalizePayment(requestId, p);
    }

    function test_Reject_MarkSettledFromNonVerifier() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        vm.prank(stranger);
        vm.expectRevert(BridgeSafeController.NotFdcVerifier.selector);
        controller.markSettled(requestId, TX_ID);
    }

    // -----------------------------------------------------------------------
    // Enclave results: identity and binding
    // -----------------------------------------------------------------------

    function test_Reject_ResultSignedByForeignKey() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        (address impostorAddr, uint256 impostorKey) = makeAddrAndKey("impostor");

        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);
        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );
        bytes32 actionId = keccak256("a");
        bytes memory sig = signAsKey(impostorKey, data, actionId, "tag", 1);

        vm.expectRevert(abi.encodeWithSelector(BridgeSafeController.BadTeeSignature.selector, impostorAddr));
        controller.submitAuthorization(data, actionId, "tag", 1, sig);
    }

    function test_Reject_TamperedResultData() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory honest = abi.encode(
            block.chainid,
            address(controller),
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );
        bytes32 actionId = keccak256("a");
        bytes memory sig = signAsTee(honest, actionId, "tag", 1);

        // Same signature, but the amount is inflated 40x on the way to the contract.
        bytes memory tampered = abi.encode(
            block.chainid,
            address(controller),
            requestId,
            r.memoRef,
            uint256(400 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );

        vm.expectRevert();
        controller.submitAuthorization(tampered, actionId, "tag", 1, sig);
    }

    function test_Reject_ResultBoundToAnotherController() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory data = abi.encode(
            block.chainid,
            address(0xDEADBEEF), // a different BridgeSafe deployment
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );
        bytes32 actionId = keccak256("a");

        vm.expectRevert(BridgeSafeController.ResultBindingMismatch.selector);
        controller.submitAuthorization(data, actionId, "tag", 1, signAsTee(data, actionId, "tag", 1));
    }

    function test_Reject_ResultBoundToAnotherChain() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory data = abi.encode(
            uint256(1), // Ethereum mainnet chain id
            address(controller),
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );
        bytes32 actionId = keccak256("a");

        vm.expectRevert(BridgeSafeController.ResultBindingMismatch.selector);
        controller.submitAuthorization(data, actionId, "tag", 1, signAsTee(data, actionId, "tag", 1));
    }

    function test_Reject_AuthorizationForWrongPayload() public {
        // The enclave answers about a different ciphertext than the one committed on
        // chain — the payload-hash mismatch case from the plan's negative tests.
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            keccak256("some other ciphertext")
        );
        bytes32 actionId = keccak256("a");

        vm.expectRevert(BridgeSafeController.ResultBindingMismatch.selector);
        controller.submitAuthorization(data, actionId, "tag", 1, signAsTee(data, actionId, "tag", 1));
    }

    function test_Reject_ResultWithFailureStatus() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );
        bytes32 actionId = keccak256("a");

        vm.expectRevert(abi.encodeWithSelector(BridgeSafeController.TeeReportedFailure.selector, uint8(0)));
        controller.submitAuthorization(data, actionId, "tag", 0, signAsTee(data, actionId, "tag", 0));
    }

    function test_Reject_MalformedSignature() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            requestId,
            r.memoRef,
            uint256(10 * XRP),
            keccak256(bytes(PAYEE_XRPL)),
            r.payloadHash
        );

        vm.expectRevert(abi.encodeWithSelector(TeeResult.BadSignatureLength.selector, uint256(10)));
        controller.submitAuthorization(data, keccak256("a"), "tag", 1, hex"00112233445566778899");
    }

    // -----------------------------------------------------------------------
    // Policy enforcement, on chain and independent of the enclave
    // -----------------------------------------------------------------------

    function test_Reject_PaymentOverPerPaymentCap() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);

        // A compromised enclave signing an over-limit authorization is still refused
        // here, because the published policy is re-checked on chain.
        (bytes memory data, bytes32 aid, bytes memory sig) = buildAuthorization(
            requestId,
            101 * XRP,
            PAYEE_XRPL
        );
        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeController.PolicyViolation.selector, "exceeds per-payment cap")
        );
        controller.submitAuthorization(data, aid, "tag", 1, sig);
    }

    function test_Reject_PaymentOverCumulativeCap() public {
        uint256 treasuryId = createBoundTreasury();

        // 5 x 100 XRP exhausts the 500 XRP lifetime cap.
        for (uint256 i = 0; i < 5; ++i) {
            uint256 id = openRequest(treasuryId);
            authorize(id, 100 * XRP, PAYEE_XRPL);
        }
        assertEq(controller.availableDrops(treasuryId), 0, "cap exhausted");

        uint256 overflow = openRequest(treasuryId);
        (bytes memory data, bytes32 aid, bytes memory sig) = buildAuthorization(
            overflow,
            1 * XRP,
            PAYEE_XRPL
        );
        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeController.PolicyViolation.selector, "exceeds cumulative cap")
        );
        controller.submitAuthorization(data, aid, "tag", 1, sig);
    }

    function test_CancellingReleasesReservedBudget() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        authorize(requestId, 100 * XRP, PAYEE_XRPL);
        assertEq(controller.availableDrops(treasuryId), 400 * XRP);

        vm.prank(treasuryOwner);
        controller.cancelRequest(requestId);
        assertEq(controller.availableDrops(treasuryId), 500 * XRP, "budget returned");
    }

    function test_Reject_InvalidPolicies() public {
        vm.startPrank(treasuryOwner);

        BridgeSafeController.Policy memory p = defaultPolicy();
        p.maxPerPaymentDrops = 0;
        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeController.InvalidPolicy.selector, "per-payment cap is zero")
        );
        controller.createTreasury{value: 0.01 ether}(p);

        p = defaultPolicy();
        p.maxTotalDrops = 1;
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeController.InvalidPolicy.selector,
                "cumulative cap below per-payment cap"
            )
        );
        controller.createTreasury{value: 0.01 ether}(p);

        p = defaultPolicy();
        p.requestTtlSeconds = 0;
        vm.expectRevert(abi.encodeWithSelector(BridgeSafeController.InvalidPolicy.selector, "ttl is zero"));
        controller.createTreasury{value: 0.01 ether}(p);

        p = defaultPolicy();
        p.requestTtlSeconds = 8 days;
        vm.expectRevert(abi.encodeWithSelector(BridgeSafeController.InvalidPolicy.selector, "ttl too long"));
        controller.createTreasury{value: 0.01 ether}(p);

        vm.stopPrank();
    }

    // -----------------------------------------------------------------------
    // Access control
    // -----------------------------------------------------------------------

    function test_Reject_UnauthorizedRequester() public {
        uint256 treasuryId = createBoundTreasury();

        vm.prank(stranger);
        vm.expectRevert(BridgeSafeController.NotTreasuryOwner.selector);
        controller.createPaymentRequest{value: 0.01 ether}(treasuryId, ciphertext());
    }

    function test_Reject_UnauthorizedSignatureRequest() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        authorize(requestId, 10 * XRP, PAYEE_XRPL);

        vm.prank(stranger);
        vm.expectRevert(BridgeSafeController.NotTreasuryOwner.selector);
        controller.requestSignature{value: 0.01 ether}(requestId);
    }

    function test_Reject_UnauthorizedCancel() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);

        vm.prank(stranger);
        vm.expectRevert(BridgeSafeController.NotTreasuryOwner.selector);
        controller.cancelRequest(requestId);
    }

    function test_Reject_NonOwnerAdminCalls() public {
        vm.startPrank(stranger);
        vm.expectRevert(BridgeSafeController.NotOwner.selector);
        controller.setTeeAddress(stranger);
        vm.expectRevert(BridgeSafeController.NotOwner.selector);
        controller.setFdcVerifier(stranger);
        vm.expectRevert(BridgeSafeController.NotOwner.selector);
        controller.setPaused(true);
        vm.stopPrank();
    }

    function test_Reject_RequestsOnUnboundTreasury() public {
        vm.prank(treasuryOwner);
        uint256 treasuryId = controller.createTreasury{value: 0.01 ether}(defaultPolicy());

        vm.prank(treasuryOwner);
        vm.expectRevert(abi.encodeWithSelector(BridgeSafeController.TreasuryNotBound.selector, treasuryId));
        controller.createPaymentRequest{value: 0.01 ether}(treasuryId, ciphertext());
    }

    function test_Reject_RebindingTreasuryAddress() public {
        uint256 treasuryId = createBoundTreasury();

        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            treasuryId,
            "rAttackerControlledAccount00001",
            controller.policyCommitment(defaultPolicy())
        );
        bytes32 actionId = keccak256("rebind");

        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeController.TreasuryAlreadyBound.selector, treasuryId)
        );
        controller.bindTreasuryAddress(data, actionId, "tag", 1, signAsTee(data, actionId, "tag", 1));
    }

    // -----------------------------------------------------------------------
    // Expiry, pause and state-machine ordering
    // -----------------------------------------------------------------------

    function test_Reject_AuthorizingExpiredRequest() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        (bytes memory data, bytes32 aid, bytes memory sig) = buildAuthorization(
            requestId,
            10 * XRP,
            PAYEE_XRPL
        );

        vm.warp(r.expiresAt + 1);
        vm.expectRevert(
            abi.encodeWithSelector(BridgeSafeController.RequestExpired.selector, requestId, r.expiresAt)
        );
        controller.submitAuthorization(data, aid, "tag", 1, sig);
    }

    function test_ExpireReleasesBudget() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        authorize(requestId, 100 * XRP, PAYEE_XRPL);
        assertEq(controller.availableDrops(treasuryId), 400 * XRP);

        vm.warp(controller.getRequest(requestId).expiresAt + 1);
        controller.expireRequest(requestId);

        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.EXPIRED));
        assertEq(controller.availableDrops(treasuryId), 500 * XRP, "budget returned");
    }

    function test_Reject_ExpiringSignedRequestBeforeGracePeriod() public {
        // A signed request still has a broadcastable blob whose LastLedgerSequence has
        // not lapsed. Releasing its budget early would let a fresh request reuse the same
        // allowance while the old payment can still land.
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 100 * XRP, TX_ID);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        vm.warp(r.expiresAt + 1);
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeController.NotYetExpired.selector,
                requestId,
                r.expiresAt + controller.SETTLEMENT_GRACE()
            )
        );
        controller.expireRequest(requestId);

        vm.warp(r.expiresAt + controller.SETTLEMENT_GRACE() + 1);
        controller.expireRequest(requestId);
        assertEq(controller.availableDrops(treasuryId), 500 * XRP);
    }

    function test_Reject_ExpiringEarly() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);

        vm.expectRevert();
        controller.expireRequest(requestId);
    }

    function test_Reject_CancellingAfterSigning() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        vm.prank(treasuryOwner);
        vm.expectRevert();
        controller.cancelRequest(requestId);
    }

    function test_Reject_SigningWithoutAuthorization() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);

        vm.prank(treasuryOwner);
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeController.WrongState.selector,
                requestId,
                BridgeSafeController.RequestState.AUTHORIZED,
                BridgeSafeController.RequestState.CREATED
            )
        );
        controller.requestSignature{value: 0.01 ether}(requestId);
    }

    function test_Reject_DoubleAuthorization() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        (bytes memory data, bytes32 aid, bytes memory sig) = buildAuthorization(
            requestId,
            10 * XRP,
            PAYEE_XRPL
        );
        controller.submitAuthorization(data, aid, "tag", 1, sig);

        // Replaying the identical authorization must not reserve the budget again.
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeController.WrongState.selector,
                requestId,
                BridgeSafeController.RequestState.CREATED,
                BridgeSafeController.RequestState.AUTHORIZED
            )
        );
        controller.submitAuthorization(data, aid, "tag", 1, sig);
        assertEq(controller.availableDrops(treasuryId), 490 * XRP, "reserved exactly once");
    }

    function test_Reject_BroadcastWithMismatchedTxId() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        bytes32 wrong = keccak256("some-other-tx");
        vm.expectRevert(abi.encodeWithSelector(BridgeSafeController.TxIdMismatch.selector, TX_ID, wrong));
        controller.reportBroadcast(requestId, wrong);
    }

    function test_Reject_DoubleBroadcastReport() public {
        // Relayer restart safety: reporting the same broadcast twice must not advance or
        // corrupt state.
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        controller.reportBroadcast(requestId, TX_ID);
        vm.expectRevert();
        controller.reportBroadcast(requestId, TX_ID);

        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.BROADCAST));
    }

    function test_PauseBlocksNewRequestsButNotSettlement() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        vm.prank(admin);
        controller.setPaused(true);

        vm.prank(treasuryOwner);
        vm.expectRevert(BridgeSafeController.ContractPaused.selector);
        controller.createPaymentRequest{value: 0.01 ether}(treasuryId, ciphertext());

        // An XRPL payment that already exists must still be provable, or pausing would
        // strand real money in an unverifiable state.
        verifier.finalizePayment(requestId, goodProof(requestId, TX_ID, 10 * XRP));
        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.SETTLED));
    }

    function test_TreasuryPauseBlocksItsRequestsOnly() public {
        uint256 a = createBoundTreasury();
        vm.prank(treasuryOwner);
        uint256 b = controller.createTreasury{value: 0.01 ether}(defaultPolicy());
        bytes memory data = abi.encode(
            block.chainid,
            address(controller),
            b,
            "rSecondTreasuryAccountAddress01",
            controller.policyCommitment(defaultPolicy())
        );
        bytes32 aid = keccak256("bind-b");
        controller.bindTreasuryAddress(data, aid, "tag", 1, signAsTee(data, aid, "tag", 1));

        vm.prank(treasuryOwner);
        controller.setTreasuryPaused(a, true);

        vm.prank(treasuryOwner);
        vm.expectRevert(BridgeSafeController.TreasuryPaused.selector);
        controller.createPaymentRequest{value: 0.01 ether}(a, ciphertext());

        vm.prank(treasuryOwner);
        controller.createPaymentRequest{value: 0.01 ether}(b, ciphertext());
    }

    // -----------------------------------------------------------------------
    // Registry binding
    // -----------------------------------------------------------------------

    function test_RegistryRejectsInstructionsFromAnyoneButTheController() public {
        registry.setEnforceSender(true);

        // The controller still works: it is the bound InstructionSender.
        uint256 treasuryId = createBoundTreasury();
        assertTrue(controller.getTreasury(treasuryId).bound);

        // A direct call from an EOA is refused by the registry.
        address[] memory tees = new address[](1);
        tees[0] = address(0x1111111111111111111111111111111111111111);

        ITeeExtensionRegistry.TeeInstructionParams memory params = ITeeExtensionRegistry
            .TeeInstructionParams({
                opType: controller.OP_TYPE_TREASURY(),
                opCommand: controller.OP_CMD_AUTHORIZE_PAYMENT(),
                message: hex"00",
                cosigners: new address[](0),
                cosignersThreshold: 0,
                claimBackAddress: address(0)
            });

        vm.prank(stranger);
        vm.expectRevert(
            abi.encodeWithSelector(MockTeeRegistry.UnregisteredSender.selector, stranger)
        );
        registry.sendInstructions(tees, params);
    }

    function test_Reject_SendingInstructionsBeforeExtensionIdIsSet() public {
        // A fresh controller that has not discovered its extension id cannot route
        // anything, so a misconfigured deployment fails loudly instead of silently
        // dispatching to no enclave.
        MockTeeRegistry fresh = new MockTeeRegistry();
        vm.prank(admin);
        BridgeSafeController c = new BridgeSafeController(fresh, fresh);

        vm.prank(treasuryOwner);
        vm.expectRevert(BridgeSafeController.ExtensionIdUnset.selector);
        c.createTreasury{value: 0.01 ether}(defaultPolicy());
    }

    function test_ExtensionIdIsSetOnce() public {
        vm.expectRevert(BridgeSafeController.ExtensionIdAlreadySet.selector);
        controller.setExtensionId();
    }

    // -----------------------------------------------------------------------
    // Policy updates
    // -----------------------------------------------------------------------

    /// @dev Tightening a policy has to stick. `confirmPolicy` is permissionless and takes
    ///      an enclave-signed result, so without a guard binding it to a *pending* request
    ///      the signed acknowledgement of an older, looser policy stays valid forever and
    ///      anyone can replay it to roll the published limits back.
    ///
    ///      This is not only a bookkeeping problem. The enclave refuses any payment whose
    ///      instruction header carries a policy commitment other than the one it currently
    ///      holds (extension.go, handleAuthorizePayment), so a rolled-back commitment also
    ///      bricks the treasury: every subsequent request is declined until the owner runs
    ///      another update, which the same replay can immediately undo.
    function test_Reject_ReplayingAnOldPolicyConfirmation() public {
        uint256 treasuryId = createBoundTreasury();

        // The owner tightens the per-payment cap from 100 XRP to 5 XRP.
        BridgeSafeController.Policy memory tight = BridgeSafeController.Policy({
            maxPerPaymentDrops: 5 * XRP,
            maxTotalDrops: 500 * XRP,
            requestTtlSeconds: 30 minutes
        });

        vm.prank(treasuryOwner);
        controller.requestPolicyUpdate{value: 0.01 ether}(treasuryId, tight);

        (bytes memory tightData, bytes32 tightAction, bytes memory tightSig) = buildPolicyConfirmation(
            treasuryId,
            tight
        );
        controller.confirmPolicy(tightData, tightAction, "tag", 1, tightSig);
        assertEq(controller.getTreasury(treasuryId).policy.maxPerPaymentDrops, 5 * XRP);

        // The enclave's acknowledgement of the ORIGINAL loose policy is still a validly
        // signed result. A stranger replays it.
        (bytes memory looseData, bytes32 looseAction, bytes memory looseSig) = buildPolicyConfirmation(
            treasuryId,
            defaultPolicy()
        );

        vm.prank(stranger);
        vm.expectRevert(BridgeSafeController.NoPendingPolicy.selector);
        controller.confirmPolicy(looseData, looseAction, "tag", 1, looseSig);

        // The tightened cap survives.
        assertEq(controller.getTreasury(treasuryId).policy.maxPerPaymentDrops, 5 * XRP);
        assertEq(
            controller.getTreasury(treasuryId).policyCommitment,
            controller.policyCommitment(tight)
        );
    }

    /// @dev A confirmation the owner never asked for is refused, even with a good
    ///      signature — the enclave should not be able to install its own limits.
    function test_Reject_PolicyConfirmationThatWasNeverRequested() public {
        uint256 treasuryId = createBoundTreasury();

        BridgeSafeController.Policy memory unrequested = BridgeSafeController.Policy({
            maxPerPaymentDrops: 10_000 * XRP,
            maxTotalDrops: 10_000 * XRP,
            requestTtlSeconds: 30 minutes
        });

        (bytes memory data, bytes32 actionId, bytes memory sig) = buildPolicyConfirmation(
            treasuryId,
            unrequested
        );

        vm.expectRevert(BridgeSafeController.NoPendingPolicy.selector);
        controller.confirmPolicy(data, actionId, "tag", 1, sig);
    }

    /// @dev One request, one confirmation. The second attempt has nothing pending.
    function test_Reject_ConfirmingTheSamePolicyUpdateTwice() public {
        uint256 treasuryId = createBoundTreasury();

        BridgeSafeController.Policy memory next = BridgeSafeController.Policy({
            maxPerPaymentDrops: 50 * XRP,
            maxTotalDrops: 500 * XRP,
            requestTtlSeconds: 30 minutes
        });

        vm.prank(treasuryOwner);
        controller.requestPolicyUpdate{value: 0.01 ether}(treasuryId, next);

        (bytes memory data, bytes32 actionId, bytes memory sig) = buildPolicyConfirmation(
            treasuryId,
            next
        );
        controller.confirmPolicy(data, actionId, "tag", 1, sig);

        vm.expectRevert(BridgeSafeController.NoPendingPolicy.selector);
        controller.confirmPolicy(data, actionId, "tag", 1, sig);
    }

    /// @dev The enclave answering with different terms than were requested is refused.
    function test_Reject_PolicyConfirmationThatDoesNotMatchTheRequest() public {
        uint256 treasuryId = createBoundTreasury();

        BridgeSafeController.Policy memory requested = BridgeSafeController.Policy({
            maxPerPaymentDrops: 5 * XRP,
            maxTotalDrops: 500 * XRP,
            requestTtlSeconds: 30 minutes
        });
        BridgeSafeController.Policy memory substituted = BridgeSafeController.Policy({
            maxPerPaymentDrops: 90 * XRP,
            maxTotalDrops: 500 * XRP,
            requestTtlSeconds: 30 minutes
        });

        vm.prank(treasuryOwner);
        controller.requestPolicyUpdate{value: 0.01 ether}(treasuryId, requested);

        (bytes memory data, bytes32 actionId, bytes memory sig) = buildPolicyConfirmation(
            treasuryId,
            substituted
        );

        vm.expectRevert(BridgeSafeController.NoPendingPolicy.selector);
        controller.confirmPolicy(data, actionId, "tag", 1, sig);
    }

    /// @dev The happy path, so the guard cannot be satisfied by simply breaking updates.
    function test_PolicyUpdateAppliesOnceTheEnclaveAcknowledgesIt() public {
        uint256 treasuryId = createBoundTreasury();

        BridgeSafeController.Policy memory next = BridgeSafeController.Policy({
            maxPerPaymentDrops: 250 * XRP,
            maxTotalDrops: 1_000 * XRP,
            requestTtlSeconds: 45 minutes
        });

        vm.prank(treasuryOwner);
        controller.requestPolicyUpdate{value: 0.01 ether}(treasuryId, next);

        // Still the old policy until the enclave confirms.
        assertEq(controller.getTreasury(treasuryId).policy.maxPerPaymentDrops, 100 * XRP);

        (bytes memory data, bytes32 actionId, bytes memory sig) = buildPolicyConfirmation(
            treasuryId,
            next
        );
        controller.confirmPolicy(data, actionId, "tag", 1, sig);

        BridgeSafeController.Treasury memory t = controller.getTreasury(treasuryId);
        assertEq(t.policy.maxPerPaymentDrops, 250 * XRP);
        assertEq(t.policy.maxTotalDrops, 1_000 * XRP);
        assertEq(t.policy.requestTtlSeconds, 45 minutes);
        assertEq(t.policyCommitment, controller.policyCommitment(next));
    }

    /// @dev A policy update may not strand the treasury below what it has already
    ///      committed to spend — `availableDrops` would underflow and revert.
    function test_Reject_PolicyUpdateBelowAlreadyReservedSpend() public {
        uint256 treasuryId = createBoundTreasury();

        uint256 requestId = openRequest(treasuryId);
        authorize(requestId, 80 * XRP, PAYEE_XRPL);

        BridgeSafeController.Policy memory tooLow = BridgeSafeController.Policy({
            maxPerPaymentDrops: 10 * XRP,
            maxTotalDrops: 50 * XRP, // below the 80 XRP already reserved
            requestTtlSeconds: 30 minutes
        });

        vm.prank(treasuryOwner);
        vm.expectRevert(
            abi.encodeWithSelector(
                BridgeSafeController.InvalidPolicy.selector,
                "cumulative cap below reserved spend"
            )
        );
        controller.requestPolicyUpdate{value: 0.01 ether}(treasuryId, tooLow);

        // The treasury remains readable.
        assertEq(controller.availableDrops(treasuryId), 500 * XRP - 80 * XRP);
    }
}
