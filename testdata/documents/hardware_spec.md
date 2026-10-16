# Distributed Edge Intelligence Hardware Specification

## 1. System Overview
This specification details the edge accelerator board specifications and runtime parameters. Documentation is available at https://gleann.ai/specs and www.open-edge.org.

## 1.1 Compute Subsystem
The compute subsystem integrates an ultra-low-power neuromorphic NPU with vector acceleration cores.

## 1.1.1 BitNet Ternary Accelerator
Ternary weights {-1, 0, +1} are executed without floating-point multiplications, reducing energy consumption by 10x.

## Figure 1. Ternary MAC Pipeline
Caption: Cycle-accurate diagram of the zero-multiplication accumulator pipeline.

## 1.1.2 Memory Hierarchy
On-chip SRAM buffers 64MB of activations directly beside the tensor cores.

## Table 1. Memory Bandwidth Measurements
| Memory Tier | Capacity | Latency | Bandwidth |
| --- | --- | --- | --- |
| L1 Scratchpad | 512 KB | 1 cycle | 4.2 TB/s |
| L2 Shared SRAM | 64 MB | 4 cycles | 1.8 TB/s |
| LPDDR5X | 16 GB | 85 cycles | 68 GB/s |

## 1.2 Thermal and Power Envelope
Peak consumption under full matrix operations is strictly throttled to 15 Watts.

## 2. Interface Specifications
High-speed PCIe Gen 4 x4 connection to the host controller.
