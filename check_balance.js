const { ethers } = require('ethers');

if (process.env.PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE !== '1') {
  console.error('blocked: this script targets Arbitrum One; set PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1 to run');
  process.exit(2);
}

const provider = new ethers.JsonRpcProvider('https://arb1.arbitrum.io/rpc');
const walletAddress = '0x742d35Cc6634C0532925a3b844Bc454e4438f44e';

const WETH = '0x82aF49447D8a07e3bd95BD0d56f35241523fBab1';
const USDC = '0xaf88d065e77c8cC2239327C5EDb3A432268e5831';

const erc20ABI = ['function balanceOf(address) view returns (uint256)'];

async function checkBalances() {
    const wethContract = new ethers.Contract(WETH, erc20ABI, provider);
    const usdcContract = new ethers.Contract(USDC, erc20ABI, provider);
    
    const wethBalance = await wethContract.balanceOf(walletAddress);
    const usdcBalance = await usdcContract.balanceOf(walletAddress);
    
    console.log('WETH Balance:', ethers.formatEther(wethBalance), 'WETH');
    console.log('USDC Balance:', ethers.formatUnits(usdcBalance, 6), 'USDC');
    console.log('\nRaw values:');
    console.log('WETH:', wethBalance.toString());
    console.log('USDC:', usdcBalance.toString());
}

checkBalances().catch(console.error);
