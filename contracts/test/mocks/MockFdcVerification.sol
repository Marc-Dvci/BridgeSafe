// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {IXRPPayment} from "@flarenetwork/flare-periphery-contracts/coston2/IXRPPayment.sol";

/// @notice Minimal stand-in for Flare's `IFdcVerification`.
/// @dev Only `verifyXRPPayment` is implemented — it is the sole method
///      `BridgeSafeFdcVerifier` calls. Implementing the full `IFdcVerification` would
///      drag in nine unrelated attestation interfaces for no test value, so the verifier
///      casts to the interface and Solidity dispatches by selector.
///
///      `verifyXRPPayment` must stay `view`: the real interface declares it `view`, so
///      the call arrives as a STATICCALL and any state write here would revert.
///
///      `accept` lets a test simulate a proof that fails Merkle verification — the case
///      that must never settle a request.
contract MockFdcVerification {
    bool public accept = true;

    function setAccept(bool _accept) external {
        accept = _accept;
    }

    function verifyXRPPayment(IXRPPayment.Proof calldata) external view returns (bool) {
        return accept;
    }
}
