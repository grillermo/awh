import asyncio
import os
from tapo import ApiClient

async def turn_off_plug():
    # 1. Configuration: Replace these variables with your actual details
    # It is recommended to use environment variables for your credentials
    ip_address = "192.168.1.86" # Replace with the plug's local IP address
    tapo_email = os.environ.get("TAPO_EMAIL", "guillermo.siliceo@gmail.com")
    tapo_password = os.environ.get("TAPO_PASSWORD", "2qEP@Pxiy32*qtd")

    try:
        # 2. Initialize the API client with your Tapo account credentials
        print("Authenticating...")
        client = ApiClient(tapo_email, tapo_password)
        
        # 3. Connect to the specific P100 device using its local IP
        device = await client.p100(ip_address)
        
        # 4. Turn off the device
        print(f"Turning off Tapo P100 at {ip_address}...")
        await device.on()
        
        print("Success: Device is now off.")
        
    except Exception as e:
        print(f"An error occurred: {e}")

if __name__ == "__main__":
    # Run the asynchronous function
    asyncio.run(turn_off_plug())
