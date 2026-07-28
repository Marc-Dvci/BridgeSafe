// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {IXRPPayment} from "@flarenetwork/flare-periphery-contracts/coston2/IXRPPayment.sol";

import {BridgeSafeTestBase} from "./BridgeSafeTestBase.sol";
import {BridgeSafeController} from "../src/BridgeSafeController.sol";
import {BridgeSafeFdcVerifier} from "../src/BridgeSafeFdcVerifier.sol";
import {MockTeeRegistry} from "./mocks/MockTeeRegistry.sol";

/// @notice The happy path, end to end, plus the state-machine invariants that hold
///         around it.
contract LifecycleTest is BridgeSafeTestBase {
    bytes32 internal constant TX_ID = keccak256("xrpl-tx-1");

    function test_FullLifecycle_CreatedToSettled() public {
        uint256 treasuryId = createBoundTreasury();

        BridgeSafeController.Treasury memory t = controller.getTreasury(treasuryId);
        assertTrue(t.bound, "treasury bound");
        assertEq(t.xrplAddressHash, keccak256(bytes(TREASURY_XRPL)), "address hash");
        assertEq(t.owner, treasuryOwner, "owner");

        uint256 amount = 25 * XRP;
        uint256 requestId = openRequest(treasuryId);
        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.CREATED));

        authorize(requestId, amount, PAYEE_XRPL);
        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.AUTHORIZED));
        assertEq(controller.availableDrops(treasuryId), 500 * XRP - amount, "budget reserved");

        sign(requestId, TX_ID);
        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.SIGNED));

        vm.prank(relayer);
        controller.reportBroadcast(requestId, TX_ID);
        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.BROADCAST));

        verifier.finalizePayment(requestId, goodProof(requestId, TX_ID, amount));

        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);
        assertEq(uint8(r.state), uint8(BridgeSafeController.RequestState.SETTLED), "settled");
        assertEq(r.xrplTxId, TX_ID, "tx id recorded");
        assertEq(r.amountDrops, amount, "amount recorded");
        assertEq(verifier.settledBy(TX_ID), requestId, "tx id consumed");

        // Budget stays consumed after settlement — the money really left.
        assertEq(controller.availableDrops(treasuryId), 500 * XRP - amount, "budget still spent");
    }

    function test_Settles_WithoutBroadcastReport() public {
        // reportBroadcast is observability only. A request must still settle if the
        // relayer never reported, otherwise a silent relayer could strand funds.
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = requestThroughSigned(treasuryId, 10 * XRP, TX_ID);

        verifier.finalizePayment(requestId, goodProof(requestId, TX_ID, 10 * XRP));
        assertEq(uint8(stateOf(requestId)), uint8(BridgeSafeController.RequestState.SETTLED));
    }

    function test_InstructionsCarryAuthenticatedHeader() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);

        MockTeeRegistry.Instruction memory ins = registry.lastInstruction();
        assertEq(ins.opType, controller.OP_TYPE_TREASURY(), "op type");
        assertEq(ins.opCommand, controller.OP_CMD_AUTHORIZE_PAYMENT(), "op command");
        assertEq(ins.sender, address(controller), "only the controller may send");

        (BridgeSafeController.InstructionHeader memory h, bytes memory payload) = abi.decode(
            ins.message,
            (BridgeSafeController.InstructionHeader, bytes)
        );
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        assertEq(h.chainId, block.chainid, "chain id bound");
        assertEq(h.controller, address(controller), "controller bound");
        assertEq(h.treasuryId, treasuryId, "treasury bound");
        assertEq(h.requestId, requestId, "request bound");
        assertEq(h.nonce, r.nonce, "nonce bound");
        assertEq(h.expiresAt, r.expiresAt, "expiry bound");
        assertEq(h.memoRef, r.memoRef, "memo ref bound");
        assertEq(keccak256(payload), r.payloadHash, "ciphertext matches commitment");
    }

    function test_NoncesAreSequentialPerTreasury() public {
        uint256 a = createBoundTreasury();

        uint256 r1 = openRequest(a);
        uint256 r2 = openRequest(a);
        uint256 r3 = openRequest(a);

        assertEq(controller.getRequest(r1).nonce, 1);
        assertEq(controller.getRequest(r2).nonce, 2);
        assertEq(controller.getRequest(r3).nonce, 3);

        // Distinct requests must never share a memo reference.
        assertTrue(controller.getRequest(r1).memoRef != controller.getRequest(r2).memoRef);
        assertTrue(controller.getRequest(r2).memoRef != controller.getRequest(r3).memoRef);
    }

    function test_MemoIsPrefixedThirtySixBytes() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 requestId = openRequest(treasuryId);
        BridgeSafeController.PaymentRequest memory r = controller.getRequest(requestId);

        bytes memory memo = controller.encodeMemo(r.memoRef);
        assertEq(memo.length, 36, "4-byte prefix + 32-byte reference");
        assertEq(bytes4(memo), controller.MEMO_PREFIX(), "prefix");
    }

    function test_TreasuryRequestsEnumerated() public {
        uint256 treasuryId = createBoundTreasury();
        uint256 r1 = openRequest(treasuryId);
        uint256 r2 = openRequest(treasuryId);

        uint256[] memory ids = controller.getTreasuryRequests(treasuryId);
        assertEq(ids.length, 2);
        assertEq(ids[0], r1);
        assertEq(ids[1], r2);
    }

    function test_CumulativeSpendAccumulatesAcrossPayments() public {
        uint256 treasuryId = createBoundTreasury();

        uint256 r1 = requestThroughSigned(treasuryId, 100 * XRP, keccak256("tx-a"));
        verifier.finalizePayment(r1, goodProof(r1, keccak256("tx-a"), 100 * XRP));

        uint256 r2 = requestThroughSigned(treasuryId, 100 * XRP, keccak256("tx-b"));
        verifier.finalizePayment(r2, goodProof(r2, keccak256("tx-b"), 100 * XRP));

        assertEq(controller.availableDrops(treasuryId), 300 * XRP, "two payments consumed");
    }
}
