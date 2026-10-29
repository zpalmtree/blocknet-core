//! Proof of Work using Argon2id
//!
//! Memory-hard PoW to resist ASICs and ensure fair mining.
//! Uses 2GB memory, making specialized hardware impractical.

use blocknet_pow_kernel as fixed_argon;

use std::ops::{Deref, DerefMut};
use std::sync::{Mutex, OnceLock};

/// Argon2id parameters for PoW
/// - Memory: 2GB (2097152 KB)
/// - Iterations: 1 (memory-hardness is the goal)
/// - Parallelism: 1 (single-threaded for fairness)
/// - Output: 32 bytes
const POW_MEMORY_KB: u32 = 2 * 1024 * 1024; // 2GB in KB
const POW_OUTPUT_LEN: usize = 32;
type PowOutput = [u8; POW_OUTPUT_LEN];

static POW_KERNEL: OnceLock<PowKernel> = OnceLock::new();

struct PowKernel {
    hasher: fixed_argon::FixedArgon2id,
    arenas: Mutex<Vec<Vec<fixed_argon::PowBlock>>>,
}

impl PowKernel {
    fn new() -> Self {
        Self {
            hasher: fixed_argon::FixedArgon2id::new(POW_MEMORY_KB),
            arenas: Mutex::new(Vec::new()),
        }
    }

    fn hash_into(&self, header: &[u8], nonce: u64, output: &mut PowOutput) -> Result<(), i32> {
        let mut arena = self.checkout_arena()?;
        hash_with_fixed_argon(&self.hasher, arena.deref_mut(), header, nonce, output)
    }

    fn checkout_arena(&self) -> Result<PowArenaGuard<'_>, i32> {
        let mut arenas = self.arenas.lock().map_err(|_| -2)?;
        let arena = arenas
            .pop()
            .unwrap_or_else(|| vec![fixed_argon::PowBlock::default(); self.hasher.block_count()]);
        Ok(PowArenaGuard {
            arenas: &self.arenas,
            arena: Some(arena),
        })
    }
}

struct PowArenaGuard<'a> {
    arenas: &'a Mutex<Vec<Vec<fixed_argon::PowBlock>>>,
    arena: Option<Vec<fixed_argon::PowBlock>>,
}

impl Deref for PowArenaGuard<'_> {
    type Target = [fixed_argon::PowBlock];

    fn deref(&self) -> &Self::Target {
        self.arena
            .as_deref()
            .expect("pow arena guard must always hold an arena")
    }
}

impl DerefMut for PowArenaGuard<'_> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        self.arena
            .as_deref_mut()
            .expect("pow arena guard must always hold an arena")
    }
}

impl Drop for PowArenaGuard<'_> {
    fn drop(&mut self) {
        if let Some(arena) = self.arena.take() {
            if let Ok(mut arenas) = self.arenas.lock() {
                arenas.push(arena);
            }
        }
    }
}

fn pow_kernel() -> &'static PowKernel {
    POW_KERNEL.get_or_init(PowKernel::new)
}

fn hash_with_fixed_argon(
    hasher: &fixed_argon::FixedArgon2id,
    arena: &mut [fixed_argon::PowBlock],
    header: &[u8],
    nonce: u64,
    output: &mut PowOutput,
) -> Result<(), i32> {
    let nonce_bytes = nonce.to_le_bytes();
    hasher
        .hash_password_into_with_memory(&nonce_bytes, header, output, arena)
        .map_err(|_| -3)
}

/// Compute Argon2id hash for proof of work
///
/// # Arguments
/// * `block_header` - Serialized block header (without nonce), used as salt
/// * `nonce` - 8-byte nonce, used as password
///
/// # Returns
/// * 32-byte hash on success, or error code
#[unsafe(no_mangle)]
pub extern "C" fn blocknet_pow_hash(
    header_ptr: *const u8,
    header_len: usize,
    nonce: u64,
    output_ptr: *mut u8,
) -> i32 {
    if header_ptr.is_null() || output_ptr.is_null() {
        return -1;
    }

    let header = unsafe { std::slice::from_raw_parts(header_ptr, header_len) };
    let mut output = [0u8; POW_OUTPUT_LEN];
    match pow_kernel().hash_into(header, nonce, &mut output) {
        Ok(()) => {
            unsafe {
                std::ptr::copy_nonoverlapping(output.as_ptr(), output_ptr, POW_OUTPUT_LEN);
            }
            0
        }
        Err(err) => err,
    }
}

/// Check if a hash meets the difficulty target
///
/// # Arguments
/// * `hash` - 32-byte hash to check
/// * `target` - 32-byte target (hash must be less than this)
///
/// # Returns
/// * 1 if hash < target (valid), 0 otherwise
#[unsafe(no_mangle)]
pub extern "C" fn blocknet_pow_check_target(hash_ptr: *const u8, target_ptr: *const u8) -> i32 {
    if hash_ptr.is_null() || target_ptr.is_null() {
        return 0;
    }

    let hash = unsafe { std::slice::from_raw_parts(hash_ptr, 32) };
    let target = unsafe { std::slice::from_raw_parts(target_ptr, 32) };

    // Compare bytes from most significant (big-endian comparison)
    for i in 0..32 {
        if hash[i] < target[i] {
            return 1; // hash < target, valid
        }
        if hash[i] > target[i] {
            return 0; // hash > target, invalid
        }
    }
    1 // hash == target, valid
}

/// Convert difficulty to target
/// Target = floor((2^256 - 1) / difficulty)
#[unsafe(no_mangle)]
pub extern "C" fn blocknet_difficulty_to_target(difficulty: u64, target_ptr: *mut u8) -> i32 {
    if target_ptr.is_null() || difficulty == 0 {
        return -1;
    }

    // Exact integer division over 256-bit numerator:
    // numerator = (2^256 - 1) = [u64::MAX, u64::MAX, u64::MAX, u64::MAX]
    // Compute quotient limbs in base 2^64 using long division by u64 divisor.
    let divisor = difficulty as u128;
    let numerator = [u64::MAX; 4];
    let mut quotient = [0u64; 4];
    let mut rem = 0u128;

    for (i, limb) in numerator.iter().enumerate() {
        let cur = (rem << 64) | (*limb as u128);
        quotient[i] = (cur / divisor) as u64;
        rem = cur % divisor;
    }

    let mut target = [0u8; 32];
    for i in 0..4 {
        let be = quotient[i].to_be_bytes();
        target[i * 8..(i + 1) * 8].copy_from_slice(&be);
    }

    unsafe {
        std::ptr::copy_nonoverlapping(target.as_ptr(), target_ptr, 32);
    }
    0
}

#[cfg(test)]
mod tests {
    use argon2::{Algorithm, Argon2, Params, Version};

    use super::*;

    fn reference_pow_hash(header: &[u8], nonce: u64, memory_kib: u32) -> [u8; 32] {
        let params = Params::new(memory_kib, 1, 1, Some(POW_OUTPUT_LEN))
            .expect("reference params should be valid");
        let reference = Argon2::new(Algorithm::Argon2id, Version::V0x13, params);
        let mut memory = vec![argon2::Block::default(); reference.params().block_count()];
        let mut output = [0u8; 32];
        reference
            .hash_password_into_with_memory(&nonce.to_le_bytes(), header, &mut output, &mut memory)
            .expect("reference hash should succeed");
        output
    }

    #[test]
    fn fixed_kernel_matches_reference_for_small_memory() {
        let headers = [
            b"12345678".as_slice(),
            b"test_block_header_data".as_slice(),
            b"headerbase0123456789abcdefghijklmnop".as_slice(),
        ];
        let memory_kib_values = [8u32, 32u32, 4096u32];
        let nonces = [0u64, 1u64, 7u64, 42u64, 1_000_003u64];

        for memory_kib in memory_kib_values {
            let hasher = fixed_argon::FixedArgon2id::new(memory_kib);
            let mut arena = vec![fixed_argon::PowBlock::default(); hasher.block_count()];

            for header in headers {
                for nonce in nonces {
                    let mut actual = [0u8; 32];
                    let expected = reference_pow_hash(header, nonce, memory_kib);

                    hash_with_fixed_argon(&hasher, &mut arena, header, nonce, &mut actual)
                        .expect("fixed kernel hash should succeed");
                    assert_eq!(
                        actual, expected,
                        "mismatch for memory_kib={memory_kib} nonce={nonce}"
                    );
                }
            }
        }
    }

    #[test]
    fn test_pow_hash() {
        let header = b"test_block_header_data";
        let nonce: u64 = 12345;
        let mut output = [0u8; 32];

        let result = blocknet_pow_hash(header.as_ptr(), header.len(), nonce, output.as_mut_ptr());

        assert_eq!(result, 0, "PoW hash should succeed");
        assert_ne!(output, [0u8; 32], "Output should not be zero");

        // Same inputs should produce same hash (deterministic)
        let mut output2 = [0u8; 32];
        blocknet_pow_hash(header.as_ptr(), header.len(), nonce, output2.as_mut_ptr());
        assert_eq!(output, output2, "PoW hash should be deterministic");

        // Different nonce should produce different hash
        let mut output3 = [0u8; 32];
        blocknet_pow_hash(
            header.as_ptr(),
            header.len(),
            nonce + 1,
            output3.as_mut_ptr(),
        );
        assert_ne!(
            output, output3,
            "Different nonce should produce different hash"
        );
    }

    #[test]
    fn test_target_check() {
        let hash = [
            0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
            0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
            0xFF, 0xFF, 0xFF, 0xFF,
        ];

        // Target with 3 leading zero bytes - hash should pass
        let target_easy = [
            0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
            0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
            0xFF, 0xFF, 0xFF, 0xFF,
        ];

        // Target with 4 leading zero bytes - hash should fail
        let target_hard = [
            0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
            0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
            0xFF, 0xFF, 0xFF, 0xFF,
        ];

        assert_eq!(
            blocknet_pow_check_target(hash.as_ptr(), target_easy.as_ptr()),
            1
        );
        assert_eq!(
            blocknet_pow_check_target(hash.as_ptr(), target_hard.as_ptr()),
            0
        );
    }

    #[test]
    fn test_difficulty_to_target() {
        let mut target = [0u8; 32];

        // difficulty=1 => max target
        blocknet_difficulty_to_target(1, target.as_mut_ptr());
        assert_eq!(target, [0xFF; 32]);

        // difficulty=2 => floor((2^256 - 1)/2) = 0x7f...ff
        blocknet_difficulty_to_target(2, target.as_mut_ptr());
        assert_eq!(target[0], 0x7F);
        assert_eq!(target[1], 0xFF);
        assert_eq!(target[31], 0xFF);

        // difficulty=256 => floor((2^256 - 1)/256) = 0x00ff...ff
        blocknet_difficulty_to_target(256, target.as_mut_ptr());
        assert_eq!(target[0], 0x00);
        assert_eq!(target[1], 0xFF);
        assert_eq!(target[31], 0xFF);
    }
}
