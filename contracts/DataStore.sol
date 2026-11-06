// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

contract DataStoreFixed {
    struct Record {
        uint256 id;
        uint256 value;
    }
    Record[] public records;

    function batchInsert(uint256[] calldata ids, uint256[] calldata values) external {
        require(ids.length == values.length, "Length mismatch");
        for (uint i = 0; i < ids.length; i++) {
            records.push(Record(ids[i], values[i]));
        }
    }

    function getCount() external view returns (uint256) {
        return records.length;
    }

    function getRecords(uint256 start, uint256 limit) external view returns (uint256[] memory, uint256[] memory) {
        uint256 end = start + limit;
        if (end > records.length) end = records.length;
        uint256 size = end - start;
        uint256[] memory ids = new uint256[](size);
        uint256[] memory values = new uint256[](size);
        for (uint i = 0; i < size; i++) {
            ids[i] = records[start + i].id;
            values[i] = records[start + i].value;
        }
        return (ids, values);
    }
}
