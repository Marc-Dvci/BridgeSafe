// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

/// @title TeeResult
/// @notice Reconstructs and verifies the signature a Flare TEE node places on an
///         `ActionResult`.
/// @dev The node computes
///
///        resultHash  = keccak256(keccak256(data) || actionId || keccak256(tag) || status)
///        payloadHash = keccak256(abi.encode("TEE_ACTION_RESULT", chainId, resultHash))
///
///      and signs `payloadHash` under the EIP-191 personal-sign prefix. The prefix and
///      layout must stay in step with go-flare-common's
///      `signing.TEEActionResult` (github.com/flare-foundation/go-flare-common/pkg/signing);
///      they are reproduced here from Flare's published FCC examples.
///
///      Binding the chain id into the payload is what stops a result signed for Coston2
///      being replayed against a deployment on another chain.
library TeeResult {
    /// @dev Domain-separation tag the TEE node signs `ActionResult` hashes under.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 internal constant TEE_ACTION_RESULT_PREFIX = bytes32("TEE_ACTION_RESULT");

    error BadSignatureLength(uint256 length);
    error BadSignatureV(uint8 v);
    error InvalidSignature();

    /// @notice Rebuild the `ActionResult.Hash()` the TEE node computed.
    /// @param _resultData Raw `ActionResult.Data` bytes returned by the extension.
    /// @param _actionId Action identifier assigned by the TEE infrastructure.
    /// @param _submissionTag Submission tag echoed by the proxy.
    /// @param _status Handler status: 0 error, 1 success, >=2 pending.
    function resultHash(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status
    ) internal pure returns (bytes32) {
        return
            keccak256(
                abi.encodePacked(
                    keccak256(_resultData),
                    _actionId,
                    keccak256(bytes(_submissionTag)),
                    _status
                )
            );
    }

    /// @notice Recover the enclave key that signed an `ActionResult`.
    /// @dev Reverts if the signature is malformed. Callers must compare the returned
    ///      address against the TEE address registered for their extension.
    function recoverSigner(
        bytes calldata _resultData,
        bytes32 _actionId,
        string calldata _submissionTag,
        uint8 _status,
        bytes calldata _signature
    ) internal view returns (address) {
        bytes32 payloadHash = keccak256(
            abi.encode(
                TEE_ACTION_RESULT_PREFIX,
                block.chainid,
                resultHash(_resultData, _actionId, _submissionTag, _status)
            )
        );
        return _recover(_ethSigned(payloadHash), _signature);
    }

    /// @notice EIP-191 personal-sign digest of a 32-byte hash.
    function _ethSigned(bytes32 _hash) private pure returns (bytes32) {
        return keccak256(abi.encodePacked("\x19Ethereum Signed Message:\n32", _hash));
    }

    /// @notice Recover the signer of a 65-byte `[r || s || v]` secp256k1 signature.
    function _recover(bytes32 _digest, bytes calldata _sig) private pure returns (address) {
        if (_sig.length != 65) revert BadSignatureLength(_sig.length);
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(_sig.offset)
            s := calldataload(add(_sig.offset, 32))
            v := byte(0, calldataload(add(_sig.offset, 64)))
        }
        if (v < 27) v += 27;
        if (v != 27 && v != 28) revert BadSignatureV(v);
        address signer = ecrecover(_digest, v, r, s);
        if (signer == address(0)) revert InvalidSignature();
        return signer;
    }
}
