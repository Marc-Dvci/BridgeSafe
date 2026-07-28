// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {Test} from "forge-std/Test.sol";
import {BridgeSafeController} from "../src/BridgeSafeController.sol";

/// @notice Proves the Go enclave and these contracts agree on the wire format.
///
/// @dev This is the seam that breaks silently. An ABI mismatch between
///      `extension/go/internal/codec` and this contract fails no build: the
///      enclave produces a correctly-signed result, the contract refuses to
///      decode it, and the symptom looks like a signing bug.
///
///      The vectors are real output from the Go encoder, written by
///      `go test ./internal/codec/` (TestWriteCrossLanguageVectors). Decoding
///      them here with the same tuple the production functions use means a drift
///      on either side shows up as a failing test.
///
///      Regenerate with:
///        cd extension/go && go test ./internal/codec/
contract CrossLanguageTest is Test {
    string internal constant VECTORS = "test/vectors/tee-results.json";

    uint256 internal constant CHAIN_ID = 114;
    address internal constant CONTROLLER = 0x00000000000000000000000000000000000c0dE1;
    uint256 internal constant TREASURY_ID = 1;
    uint256 internal constant REQUEST_ID = 42;

    function _vector(string memory key) internal view returns (bytes memory) {
        string memory json = vm.readFile(VECTORS);
        return vm.parseJsonBytes(json, string.concat(".", key));
    }

    function _bytes32Vector(string memory key) internal view returns (bytes32) {
        string memory json = vm.readFile(VECTORS);
        return vm.parseJsonBytes32(json, string.concat(".", key));
    }

    function test_GoEncodedBindTreasury_DecodesHere() public view {
        (
            uint256 chainId,
            address controller,
            uint256 treasuryId,
            string memory xrplAddress,
            bytes32 policyCommitment
        ) = abi.decode(_vector("bindTreasury"), (uint256, address, uint256, string, bytes32));

        assertEq(chainId, CHAIN_ID, "chain id");
        assertEq(controller, CONTROLLER, "controller");
        assertEq(treasuryId, TREASURY_ID, "treasury id");
        assertEq(xrplAddress, "rafZ4XSb7yjk5Rptmu9iTYLkUQBhznDuPf", "xrpl address");
        assertEq(policyCommitment, _bytes32Vector("policyCommitment"), "policy commitment");
    }

    function test_GoEncodedAuthorization_DecodesHere() public view {
        (
            uint256 chainId,
            address controller,
            uint256 requestId,
            bytes32 memoRef,
            uint256 amountDrops,
            bytes32 destinationHash,
            bytes32 payloadHash
        ) = abi.decode(
                _vector("authorization"),
                (uint256, address, uint256, bytes32, uint256, bytes32, bytes32)
            );

        assertEq(chainId, CHAIN_ID, "chain id");
        assertEq(controller, CONTROLLER, "controller");
        assertEq(requestId, REQUEST_ID, "request id");
        assertEq(memoRef, _bytes32Vector("memoRef"), "memo ref");
        assertEq(amountDrops, 25_000_000, "amount");
        assertEq(destinationHash, _bytes32Vector("destinationHash"), "destination hash");
        assertEq(payloadHash, _bytes32Vector("payloadHash"), "payload hash");
    }

    function test_GoEncodedSignedPayment_DecodesHere() public view {
        (
            uint256 chainId,
            address controller,
            uint256 requestId,
            bytes32 memoRef,
            bytes32 expectedTxId,
            bytes memory signedTxBlob
        ) = abi.decode(
                _vector("signedPayment"),
                (uint256, address, uint256, bytes32, bytes32, bytes)
            );

        assertEq(chainId, CHAIN_ID, "chain id");
        assertEq(controller, CONTROLLER, "controller");
        assertEq(requestId, REQUEST_ID, "request id");
        assertEq(memoRef, _bytes32Vector("memoRef"), "memo ref");
        assertEq(expectedTxId, _bytes32Vector("txId"), "expected tx id");
        assertGt(signedTxBlob.length, 0, "blob present");
        // Every BridgeSafe blob is an XRPL Payment: TransactionType field 0x12, value 0x0000.
        assertEq(bytes3(signedTxBlob), bytes3(hex"120000"), "blob is not a Payment");
    }

    function test_GoEncodedConfirmPolicy_DecodesHere() public view {
        (
            uint256 chainId,
            address controller,
            uint256 treasuryId,
            BridgeSafeController.Policy memory policy,
            bytes32 commitment
        ) = abi.decode(
                _vector("confirmPolicy"),
                (uint256, address, uint256, BridgeSafeController.Policy, bytes32)
            );

        assertEq(chainId, CHAIN_ID, "chain id");
        assertEq(controller, CONTROLLER, "controller");
        assertEq(treasuryId, TREASURY_ID, "treasury id");
        assertEq(policy.maxPerPaymentDrops, 100_000_000, "per-payment cap");
        assertEq(policy.maxTotalDrops, 500_000_000, "cumulative cap");
        assertEq(policy.requestTtlSeconds, 1800, "ttl");
        assertEq(commitment, _bytes32Vector("policyCommitment"), "commitment");
    }

    function test_GoEncodedFailure_DecodesHere() public view {
        (uint256 chainId, address controller, uint256 requestId, string memory reason) = abi.decode(
            _vector("failure"),
            (uint256, address, uint256, string)
        );

        assertEq(chainId, CHAIN_ID, "chain id");
        assertEq(controller, CONTROLLER, "controller");
        assertEq(requestId, REQUEST_ID, "request id");
        assertEq(reason, "destination is not a valid r-address", "reason");
    }

    /// @notice The policy commitment must be identical in both languages.
    /// @dev If Go and Solidity hashed a policy differently, the enclave would
    ///      reject every instruction the contract sent, so this is worth pinning
    ///      independently of the decode tests above.
    function test_PolicyCommitment_MatchesGo() public view {
        BridgeSafeController.Policy memory policy = BridgeSafeController.Policy({
            maxPerPaymentDrops: 100_000_000,
            maxTotalDrops: 500_000_000,
            requestTtlSeconds: 1800
        });
        assertEq(
            keccak256(abi.encode(policy)),
            _bytes32Vector("policyCommitment"),
            "Go and Solidity disagree about a policy's commitment"
        );
    }
}
