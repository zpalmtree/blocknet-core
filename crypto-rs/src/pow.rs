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
    arenas: Mutex<Vec<PowArena>>,
}

impl PowKernel {
    fn new() -> Self {
        Self {
            hasher: host_kernel()
                .and_then(|kernel| fixed_argon::FixedArgon2id::with_kernel(POW_MEMORY_KB, kernel).ok())
                .unwrap_or_else(|| fixed_argon::FixedArgon2id::new(POW_MEMORY_KB)),
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
            .unwrap_or_else(|| PowArena::new(self.hasher.block_count()));
        Ok(PowArenaGuard {
            arenas: &self.arenas,
            arena: Some(arena),
        })
    }
}

/// Compression path override for this host, or `None` for the kernel crate's
/// default dispatch. AMD Zen 4/Zen 5 (family 19h/1Ah) with AVX-512 use the
/// streaming kernels Seine mines with and the pool verifies with on those
/// CPUs. The ISA support check stays mandatory.
fn host_kernel() -> Option<fixed_argon::CpuKernel> {
    let (vendor, family) = cpu_vendor_family()?;
    if vendor != *b"AuthenticAMD" {
        return None;
    }
    // Zen 4's far L2 prefetch costs Zen 5 1-3%, so family 1Ah keeps the
    // plain streaming kernel.
    let preferred: &[fixed_argon::CpuKernel] = match family {
        0x19 => &[
            fixed_argon::CpuKernel::Avx512256StreamL2,
            fixed_argon::CpuKernel::Avx512256Stream,
        ],
        0x1a => &[fixed_argon::CpuKernel::Avx512256Stream],
        _ => &[],
    };
    preferred
        .iter()
        .copied()
        .find(|kernel| kernel.is_supported())
}

#[cfg(target_arch = "x86_64")]
fn cpu_vendor_family() -> Option<([u8; 12], u32)> {
    use std::arch::x86_64::__cpuid;
    // Safety: CPUID leaves 0 and 1 exist on every x86_64 processor. Newer
    // toolchains made `__cpuid` safe; older ones still require the block.
    #[allow(unused_unsafe)]
    let (leaf0, leaf1) = unsafe { (__cpuid(0), __cpuid(1)) };
    let mut vendor = [0u8; 12];
    vendor[..4].copy_from_slice(&leaf0.ebx.to_le_bytes());
    vendor[4..8].copy_from_slice(&leaf0.edx.to_le_bytes());
    vendor[8..].copy_from_slice(&leaf0.ecx.to_le_bytes());
    let base_family = (leaf1.eax >> 8) & 0xf;
    let family = if base_family == 0xf {
        base_family + ((leaf1.eax >> 20) & 0xff)
    } else {
        base_family
    };
    Some((vendor, family))
}

#[cfg(not(target_arch = "x86_64"))]
fn cpu_vendor_family() -> Option<([u8; 12], u32)> {
    None
}

/// A 2 GiB Argon2 arena. On Linux it is an anonymous mapping hinted for
/// transparent hugepages: Argon2's data-dependent reads across 2 GiB miss the
/// TLB on almost every block with 4 KiB pages, which roughly doubles the hash
/// time. Hosts with THP set to `madvise` only back memory that asks for it.
enum PowArena {
    #[cfg(target_os = "linux")]
    Mapped(MappedArena),
    Heap(Vec<fixed_argon::PowBlock>),
}

impl PowArena {
    fn new(block_count: usize) -> Self {
        #[cfg(target_os = "linux")]
        if let Some(arena) = MappedArena::new(block_count) {
            return PowArena::Mapped(arena);
        }
        PowArena::Heap(vec![fixed_argon::PowBlock::default(); block_count])
    }

    /// Writes one byte per 4 KiB page so the first hash after startup does
    /// not pay for half a million page faults on the block path.
    fn prefault(&mut self) {
        let blocks = self.deref_mut();
        let bytes = std::mem::size_of_val(blocks);
        let base = blocks.as_mut_ptr() as *mut u8;
        for offset in (0..bytes).step_by(4096) {
            // Safety: offset < bytes, inside this arena's allocation.
            unsafe { std::ptr::write_volatile(base.add(offset), 0) };
        }
    }
}

impl Deref for PowArena {
    type Target = [fixed_argon::PowBlock];

    fn deref(&self) -> &Self::Target {
        match self {
            #[cfg(target_os = "linux")]
            PowArena::Mapped(arena) => arena.as_slice(),
            PowArena::Heap(arena) => arena,
        }
    }
}

impl DerefMut for PowArena {
    fn deref_mut(&mut self) -> &mut Self::Target {
        match self {
            #[cfg(target_os = "linux")]
            PowArena::Mapped(arena) => arena.as_mut_slice(),
            PowArena::Heap(arena) => arena,
        }
    }
}

#[cfg(target_os = "linux")]
struct MappedArena {
    ptr: std::ptr::NonNull<fixed_argon::PowBlock>,
    mapped_len: usize,
    block_count: usize,
}

// Safety: the arena exclusively owns its private anonymous mapping.
#[cfg(target_os = "linux")]
unsafe impl Send for MappedArena {}

#[cfg(target_os = "linux")]
impl MappedArena {
    const HUGE_2M: usize = 2 * 1024 * 1024;

    fn new(block_count: usize) -> Option<Self> {
        let byte_len = block_count.checked_mul(std::mem::size_of::<fixed_argon::PowBlock>())?;
        let mapped_len = byte_len.checked_add(Self::HUGE_2M - 1)? / Self::HUGE_2M * Self::HUGE_2M;
        // Safety: a fresh private anonymous mapping, owned by the result.
        let ptr = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                mapped_len,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
                -1,
                0,
            )
        };
        if ptr == libc::MAP_FAILED {
            return None;
        }
        // Best effort: without THP the mapping still works on 4 KiB pages.
        unsafe { libc::madvise(ptr, mapped_len, libc::MADV_HUGEPAGE) };
        Some(Self {
            ptr: std::ptr::NonNull::new(ptr as *mut fixed_argon::PowBlock)?,
            mapped_len,
            block_count,
        })
    }

    fn as_slice(&self) -> &[fixed_argon::PowBlock] {
        // Safety: the mapping holds at least block_count blocks, is page
        // aligned, and zeroed memory is a valid PowBlock (plain u64s).
        unsafe { std::slice::from_raw_parts(self.ptr.as_ptr(), self.block_count) }
    }

    fn as_mut_slice(&mut self) -> &mut [fixed_argon::PowBlock] {
        // Safety: as above, and &mut self gives exclusive access.
        unsafe { std::slice::from_raw_parts_mut(self.ptr.as_ptr(), self.block_count) }
    }
}

#[cfg(target_os = "linux")]
impl Drop for MappedArena {
    fn drop(&mut self) {
        // Safety: ptr/mapped_len describe exactly the mapping made in new().
        unsafe { libc::munmap(self.ptr.as_ptr() as *mut libc::c_void, self.mapped_len) };
    }
}

struct PowArenaGuard<'a> {
    arenas: &'a Mutex<Vec<PowArena>>,
    arena: Option<PowArena>,
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

/// Allocate and fault in one PoW arena ahead of time, so the first block
/// verified after startup runs at steady-state speed.
///
/// # Returns
/// * 0 on success, or error code
#[unsafe(no_mangle)]
pub extern "C" fn blocknet_pow_warm() -> i32 {
    let kernel = pow_kernel();
    match kernel.arenas.lock() {
        Ok(arenas) if !arenas.is_empty() => return 0,
        Ok(_) => {}
        Err(_) => return -2,
    }
    let mut arena = PowArena::new(kernel.hasher.block_count());
    arena.prefault();
    match kernel.arenas.lock() {
        Ok(mut arenas) => {
            arenas.push(arena);
            0
        }
        Err(_) => -2,
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

    fn host_hasher(memory_kib: u32) -> fixed_argon::FixedArgon2id {
        host_kernel()
            .and_then(|kernel| fixed_argon::FixedArgon2id::with_kernel(memory_kib, kernel).ok())
            .unwrap_or_else(|| fixed_argon::FixedArgon2id::new(memory_kib))
    }

    #[test]
    fn host_kernel_matches_reference_for_small_memory() {
        let header = b"test_block_header_data".as_slice();
        for memory_kib in [8u32, 32, 4096] {
            let hasher = host_hasher(memory_kib);
            let mut arena = vec![fixed_argon::PowBlock::default(); hasher.block_count()];
            for nonce in [0u64, 7, 1_000_003] {
                let mut actual = [0u8; 32];
                hash_with_fixed_argon(&hasher, &mut arena, header, nonce, &mut actual)
                    .expect("host kernel hash should succeed");
                assert_eq!(
                    actual,
                    reference_pow_hash(header, nonce, memory_kib),
                    "mismatch for {} memory_kib={memory_kib} nonce={nonce}",
                    hasher.isa_label()
                );
            }
        }
    }

    /// Full 2 GiB parity of the production path against the reference
    /// argon2 crate. Needs ~4 GiB of memory, so run it explicitly:
    /// `cargo test --release -- --ignored production_hash_matches_reference`.
    #[test]
    #[ignore]
    fn production_hash_matches_reference() {
        let header = b"headerbase0123456789abcdefghijklmnop".as_slice();
        let nonce = 7_598_379_899_254_000_019u64;
        let mut actual = [0u8; 32];
        assert_eq!(
            blocknet_pow_hash(header.as_ptr(), header.len(), nonce, actual.as_mut_ptr()),
            0
        );
        assert_eq!(actual, reference_pow_hash(header, nonce, POW_MEMORY_KB));
    }

    #[test]
    fn arena_backings_match_reference() {
        let memory_kib = 4096u32;
        let hasher = host_hasher(memory_kib);
        let header = b"headerbase0123456789abcdefghijklmnop".as_slice();
        let mut arena = PowArena::new(hasher.block_count());
        #[cfg(target_os = "linux")]
        assert!(matches!(arena, PowArena::Mapped(_)));
        arena.prefault();
        let mut heap = PowArena::Heap(vec![fixed_argon::PowBlock::default(); hasher.block_count()]);
        for nonce in [0u64, 42, 1_000_003] {
            let expected = reference_pow_hash(header, nonce, memory_kib);
            for arena in [&mut arena, &mut heap] {
                let mut actual = [0u8; 32];
                hash_with_fixed_argon(&hasher, arena.deref_mut(), header, nonce, &mut actual)
                    .expect("fixed kernel hash should succeed");
                assert_eq!(actual, expected, "mismatch for nonce={nonce}");
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
