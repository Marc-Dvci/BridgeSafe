// SPDX-License-Identifier: MIT
pragma solidity >=0.7.6 <0.9;

// Minimal local interface for the Flare TEE machine registry, which lives on the same
// FlareTeeManager diamond as ITeeExtensionRegistry.
//
// Replace with the canonical import once flare-smart-contracts-v2 ships as a package:
//   import { ITeeMachineRegistry } from
//     "flare-smart-contracts-v2/contracts/userInterfaces/tee/ITeeMachineRegistry.sol";
interface ITeeMachineRegistry {
    /// @notice Pick `_count` TEE machine addresses currently serving `_extensionId`.
    /// @dev Pass `_count > 1` to fan a single instruction out to multiple enclaves.
    function getRandomTeeIds(
        uint256 _extensionId,
        uint256 _count
    ) external view returns (address[] memory);
}
