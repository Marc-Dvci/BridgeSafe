// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {ContractRegistry} from "@flarenetwork/flare-periphery-contracts/coston2/ContractRegistry.sol";
import {IFdcVerification} from "@flarenetwork/flare-periphery-contracts/coston2/IFdcVerification.sol";
import {IXRPPayment} from "@flarenetwork/flare-periphery-contracts/coston2/IXRPPayment.sol";
import {BridgeSafeController} from "./BridgeSafeController.sol";

/// @title BridgeSafeFdcVerifier
/// @notice Turns a Flare Data Connector proof of an XRPL payment into a settled
///         BridgeSafe request.
///
/// @dev This is deliberately a separate, small contract. Settlement is the security-
///      critical half of the system, and keeping it isolated means a judge — or an
///      auditor — can read every condition that lets value be marked as delivered in one
///      screenful, without wading through treasury bookkeeping.
///
///      A request reaches `SETTLED` only if all of the following hold:
///
///        1. `IFdcVerification.verifyXRPPayment` accepts the Merkle proof against the
///           voting round's on-chain root.
///        2. The attestation type is `XRPPayment` and the source is the expected XRPL
///           network, so a proof about a different chain cannot be substituted.
///        3. The XRPL transaction succeeded (`status == 0`).
///        4. The funds left the treasury's own r-address.
///        5. They arrived at the destination the enclave authorized.
///        6. The received amount equals the authorized amount exactly.
///        7. The first memo is exactly `"BSF1" || memoRef` for this request.
///        8. That XRPL transaction id has never settled any request before.
///
///      Condition 8 is what makes one payment settle one request. Conditions 4-7 are what
///      stop an unrelated payment — including one the treasury made for other reasons —
///      from being passed off as fulfilling a BridgeSafe request.
contract BridgeSafeFdcVerifier {
    /// @dev Attestation type id for `XRPPayment`, right-padded ASCII.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant ATTESTATION_TYPE_XRP_PAYMENT = bytes32("XRPPayment");

    /// @notice XRPL success status in an FDC payment response.
    uint8 public constant XRPL_STATUS_SUCCESS = 0;

    /// @notice The controller whose requests this contract settles.
    BridgeSafeController public immutable CONTROLLER;

    /// @notice Expected FDC source id, e.g. `bytes32("testXRP")` for XRPL Testnet.
    /// @dev Immutable so the accepted network can never be widened after deployment.
    bytes32 public immutable EXPECTED_SOURCE_ID;

    /// @notice Optional fixed `IFdcVerification` address.
    /// @dev Zero on real networks, where the address is resolved through Flare's
    ///      `ContractRegistry` at call time. A non-zero value is only used by unit tests
    ///      and local chains that have no registry. It is immutable, so a deployed
    ///      verifier's trust anchor cannot be repointed after the fact.
    address public immutable FDC_VERIFICATION_OVERRIDE;

    /// @notice XRPL transaction ids already consumed, mapped to the request they settled.
    mapping(bytes32 => uint256) public settledBy;

    event PaymentFinalized(
        uint256 indexed requestId,
        bytes32 indexed xrplTxId,
        uint256 amountDrops,
        uint64 votingRound
    );

    error ZeroAddress();
    error InvalidFdcProof();
    error WrongAttestationType(bytes32 expected, bytes32 actual);
    error WrongSourceId(bytes32 expected, bytes32 actual);
    error XrplTransactionFailed(uint8 status);
    error TxIdAlreadyUsed(bytes32 txId, uint256 settledRequestId);
    error RequestNotBroadcastable(uint256 requestId, BridgeSafeController.RequestState state);
    error SourceAccountMismatch(bytes32 expected, bytes32 actual);
    error DestinationMismatch(bytes32 expected, bytes32 actual);
    error AmountMismatch(uint256 expected, int256 actual);
    error MissingMemo();
    error MemoMismatch(bytes expected, bytes actual);

    /// @param _controller BridgeSafe controller to settle requests on.
    /// @param _expectedSourceId FDC source id, `bytes32("testXRP")` on Coston2.
    /// @param _fdcVerificationOverride Zero on real networks. See the field docs.
    constructor(
        BridgeSafeController _controller,
        bytes32 _expectedSourceId,
        address _fdcVerificationOverride
    ) {
        if (address(_controller) == address(0)) revert ZeroAddress();
        if (_expectedSourceId == bytes32(0)) revert ZeroAddress();
        CONTROLLER = _controller;
        EXPECTED_SOURCE_ID = _expectedSourceId;
        FDC_VERIFICATION_OVERRIDE = _fdcVerificationOverride;
    }

    /// @notice Settle a BridgeSafe request with an FDC proof of its XRPL payment.
    /// @dev Permissionless by design. The proof is the authority; whoever pays the gas to
    ///      deliver it is irrelevant, and letting anyone finalize means a request cannot
    ///      be stranded by an uncooperative relayer.
    /// @param _requestId BridgeSafe request the payment fulfils.
    /// @param _proof FDC `XRPPayment` proof, as returned by the Data Availability layer.
    function finalizePayment(uint256 _requestId, IXRPPayment.Proof calldata _proof) external {
        // 1. The proof must be in the voting round's Merkle tree.
        if (!fdcVerification().verifyXRPPayment(_proof)) revert InvalidFdcProof();

        // 2. It must be the attestation type and network we expect.
        if (_proof.data.attestationType != ATTESTATION_TYPE_XRP_PAYMENT) {
            revert WrongAttestationType(ATTESTATION_TYPE_XRP_PAYMENT, _proof.data.attestationType);
        }
        if (_proof.data.sourceId != EXPECTED_SOURCE_ID) {
            revert WrongSourceId(EXPECTED_SOURCE_ID, _proof.data.sourceId);
        }

        // 3. The XRPL transaction must have succeeded.
        if (_proof.data.responseBody.status != XRPL_STATUS_SUCCESS) {
            revert XrplTransactionFailed(_proof.data.responseBody.status);
        }

        // 8. One XRPL payment settles at most one request, ever.
        bytes32 txId = _proof.data.requestBody.transactionId;
        uint256 prior = settledBy[txId];
        if (prior != 0) revert TxIdAlreadyUsed(txId, prior);

        // 4-7. The payment must match what this request authorized.
        _checkAgainstRequest(_requestId, _proof.data.responseBody);

        // Record before the external call: this contract is the only settler, and the
        // write closes the replay window regardless of what the controller does.
        settledBy[txId] = _requestId;

        CONTROLLER.markSettled(_requestId, txId);

        emit PaymentFinalized(
            _requestId,
            txId,
            uint256(_proof.data.responseBody.receivedAmount),
            _proof.data.votingRound
        );
    }

    /// @notice The `IFdcVerification` instance proofs are checked against.
    function fdcVerification() public view returns (IFdcVerification) {
        if (FDC_VERIFICATION_OVERRIDE != address(0)) {
            return IFdcVerification(FDC_VERIFICATION_OVERRIDE);
        }
        return ContractRegistry.getFdcVerification();
    }

    /// @notice Whether an XRPL transaction id has already settled a request.
    function isTxIdUsed(bytes32 _txId) external view returns (bool) {
        return settledBy[_txId] != 0;
    }

    /// @dev Compare a verified FDC response against the request's recorded expectation.
    function _checkAgainstRequest(
        uint256 _requestId,
        IXRPPayment.ResponseBody calldata _body
    ) private view {
        (
            bytes32 sourceAddressHash,
            bytes32 destinationHash,
            uint256 amountDrops,
            bytes memory expectedMemo,
            BridgeSafeController.RequestState state
        ) = CONTROLLER.settlementExpectation(_requestId);

        if (
            state != BridgeSafeController.RequestState.SIGNED &&
            state != BridgeSafeController.RequestState.BROADCAST
        ) {
            revert RequestNotBroadcastable(_requestId, state);
        }

        // 4. Funds left the treasury's own account.
        if (_body.sourceAddressHash != sourceAddressHash) {
            revert SourceAccountMismatch(sourceAddressHash, _body.sourceAddressHash);
        }

        // 5. They reached the authorized destination.
        if (_body.receivingAddressHash != destinationHash) {
            revert DestinationMismatch(destinationHash, _body.receivingAddressHash);
        }

        // 6. The amount matches exactly. `receivedAmount` is signed in the FDC response,
        //    so reject anything non-positive before comparing.
        if (_body.receivedAmount <= 0 || uint256(_body.receivedAmount) != amountDrops) {
            revert AmountMismatch(amountDrops, _body.receivedAmount);
        }

        // 7. The memo binds the payment to this specific request.
        if (!_body.hasMemoData) revert MissingMemo();
        if (keccak256(_body.firstMemoData) != keccak256(expectedMemo)) {
            revert MemoMismatch(expectedMemo, _body.firstMemoData);
        }
    }
}
