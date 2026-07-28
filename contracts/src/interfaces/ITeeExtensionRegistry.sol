// SPDX-License-Identifier: MIT
pragma solidity >=0.7.6 <0.9;

// Minimal local interface for the Flare TEE extension registry, which lives on the
// FlareTeeManager diamond. Mirrors the surface published in Flare's own FCC example
// repositories (flare-foundation/fce-extension-scaffold, fce-weather-insurance).
//
// Replace with the canonical import once flare-smart-contracts-v2 ships as a package:
//   import { ITeeExtensionRegistry } from
//     "flare-smart-contracts-v2/contracts/userInterfaces/tee/ITeeExtensionRegistry.sol";
interface ITeeExtensionRegistry {
    /// @notice Parameters of a single instruction routed to a TEE extension.
    /// @param opType Operation group selector, e.g. bytes32("TREASURY").
    /// @param opCommand Sub-command within the group, e.g. bytes32("SIGN_XRPL_PAYMENT").
    /// @param message Opaque payload handed to the extension. ABI- or ECIES-encoded.
    /// @param cosigners Optional additional signers required for the result.
    /// @param cosignersThreshold Minimum number of cosigners that must sign.
    /// @param claimBackAddress Address refunded any unused instruction fee.
    struct TeeInstructionParams {
        bytes32 opType;
        bytes32 opCommand;
        bytes message;
        address[] cosigners;
        uint64 cosignersThreshold;
        address claimBackAddress;
    }

    /// @notice Submit an instruction to the TEE machines serving an extension.
    /// @dev The registry rejects any caller that is not the InstructionSender address
    ///      bound to the extension at registration time.
    function sendInstructions(
        address[] calldata _teeIds,
        TeeInstructionParams calldata _instructionParams
    ) external payable returns (bytes32 _instructionId);

    /// @notice One past the highest assigned public extension id.
    function nextPublicExtensionId() external view returns (uint256);

    /// @notice The InstructionSender address bound to `_extensionId`.
    function getTeeExtensionInstructionsSender(
        uint256 _extensionId
    ) external view returns (address);
}
