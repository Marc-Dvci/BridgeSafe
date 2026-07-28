// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import {ITeeExtensionRegistry} from "../../src/interfaces/ITeeExtensionRegistry.sol";
import {ITeeMachineRegistry} from "../../src/interfaces/ITeeMachineRegistry.sol";

/// @notice Stand-in for the FlareTeeManager diamond.
/// @dev Records every instruction so tests can assert on what the controller actually
///      asked the enclave to do, which is where most binding bugs would show up.
contract MockTeeRegistry is ITeeExtensionRegistry, ITeeMachineRegistry {
    struct Instruction {
        bytes32 opType;
        bytes32 opCommand;
        bytes message;
        address sender;
        uint256 value;
        address claimBackAddress;
    }

    Instruction[] public instructions;

    address public boundSender;
    uint256 public boundExtensionId = 0x10000;
    address public teeMachine = 0x1111111111111111111111111111111111111111;

    /// @notice Set to true to simulate the registry rejecting an unregistered caller.
    bool public enforceSender;

    error UnregisteredSender(address caller);

    function bindSender(address _sender) external {
        boundSender = _sender;
    }

    function setEnforceSender(bool _on) external {
        enforceSender = _on;
    }

    function setTeeMachine(address _machine) external {
        teeMachine = _machine;
    }

    function setBoundExtensionId(uint256 _id) external {
        boundExtensionId = _id;
    }

    function sendInstructions(
        address[] calldata,
        TeeInstructionParams calldata _params
    ) external payable returns (bytes32) {
        if (enforceSender && msg.sender != boundSender) revert UnregisteredSender(msg.sender);

        instructions.push(
            Instruction({
                opType: _params.opType,
                opCommand: _params.opCommand,
                message: _params.message,
                sender: msg.sender,
                value: msg.value,
                claimBackAddress: _params.claimBackAddress
            })
        );
        return keccak256(abi.encode(instructions.length, _params.opCommand));
    }

    function nextPublicExtensionId() external view returns (uint256) {
        return boundExtensionId + 1;
    }

    function getTeeExtensionInstructionsSender(uint256 _extensionId) external view returns (address) {
        return _extensionId == boundExtensionId ? boundSender : address(0);
    }

    function getRandomTeeIds(uint256, uint256 _count) external view returns (address[] memory ids) {
        ids = new address[](_count);
        for (uint256 i = 0; i < _count; ++i) {
            ids[i] = teeMachine;
        }
    }

    // --- test helpers ---

    function instructionCount() external view returns (uint256) {
        return instructions.length;
    }

    function lastInstruction() external view returns (Instruction memory) {
        return instructions[instructions.length - 1];
    }

    function instructionAt(uint256 _i) external view returns (Instruction memory) {
        return instructions[_i];
    }
}
